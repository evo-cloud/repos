package repos

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// EventHandler handles events from Dispatcher.Run.
type EventHandler interface {
	HandleEvent(ctx context.Context, event DispatcherEvent)
}

// EventHandlerFunc is func form of EventHandler.
type EventHandlerFunc func(context.Context, DispatcherEvent)

// HandleEvent implements EventHandler.
func (f EventHandlerFunc) HandleEvent(ctx context.Context, event DispatcherEvent) {
	f(ctx, event)
}

// DispatcherEvent is the abstract of dispatcher events.
type DispatcherEvent interface {
	Dispatcher() *Dispatcher
	Graph() *TaskGraph
}

// DispatcherStartEvent is the event when Dispatcher.Run starts.
type DispatcherStartEvent struct {
	dispatcherEventBase
	NumWorkers int
}

// DispatcherEndEvent is the event when Dispatcher.Run ends.
type DispatcherEndEvent struct {
	dispatcherEventBase
	Err error
}

// TaskStartEvent is the event indicates a task is enqueued.
type TaskStartEvent struct {
	dispatcherEventBase
	Task   *Task
	Worker int
}

// TaskCompleteEvent is the event indicates a task is completed.
type TaskCompleteEvent struct {
	dispatcherEventBase
	Task *Task
}

// TaskResult contains persistable result of a task.
type TaskResult struct {
	SuccessBuildStartTime int64
	SuccessBuildEndTime   int64
	StartTime             int64
	EndTime               int64
	Skipped               bool
	Err                   *string
}

// Dispatcher dispatches tasks.
type Dispatcher struct {
	Graph        *TaskGraph
	DataDir      string
	OutBaseDir   string
	CacheDir     string
	LogDir       string
	NumWorkers   int
	EventHandler EventHandler

	toolsLock       sync.RWMutex
	registeredTools map[string]*ExtTool
}

type execution struct {
	dispatcher   *Dispatcher
	graph        *TaskGraph
	runningCount int
	numWorkers   int
	requestCh    chan *Task
	resultCh     chan *Task
	eventCh      chan DispatcherEvent
	logger       *log.Logger
}

type dispatcherEventBaseAccessor interface {
	eventBase() *dispatcherEventBase
}

type dispatcherEventBase struct {
	dispatcher *Dispatcher
	graph      *TaskGraph
}

func (e *dispatcherEventBase) Dispatcher() *Dispatcher {
	return e.dispatcher
}

func (e *dispatcherEventBase) Graph() *TaskGraph {
	return e.graph
}

func (e *dispatcherEventBase) eventBase() *dispatcherEventBase {
	return e
}

// NewDispatcher creates a Dispatcher with TaskGraph.
func NewDispatcher(g *TaskGraph) *Dispatcher {
	return &Dispatcher{
		Graph:           g,
		DataDir:         g.Repo.dataDir,
		OutBaseDir:      g.Repo.OutDir(),
		CacheDir:        filepath.Join(g.Repo.dataDir, cacheFolderName),
		LogDir:          g.Repo.LogDir(),
		registeredTools: make(map[string]*ExtTool),
	}
}

// Run executes tasks.
func (d *Dispatcher) Run(ctx context.Context) error {
	os.MkdirAll(d.LogDir, 0755)
	logFn := filepath.Join(d.LogDir, "_.log")
	logFile, err := os.Create(logFn)
	if err != nil {
		return fmt.Errorf("create log file %q error: %w", logFn, err)
	}
	defer logFile.Close()

	x := execution{
		dispatcher: d,
		graph:      d.Graph,
		numWorkers: d.NumWorkers,
		logger:     log.New(logFile, "", log.LstdFlags),
	}
	if x.numWorkers == 0 {
		x.numWorkers = runtime.NumCPU()
	}

	x.requestCh = make(chan *Task, x.numWorkers)
	x.resultCh = make(chan *Task, x.numWorkers)
	x.eventCh = make(chan DispatcherEvent, x.numWorkers)

	return x.run(ctx)
}

func (x *execution) haveWorkToDo() bool {
	return x.graph.CompleteList.Len() < len(x.graph.Tasks)
}

func (x *execution) run(ctx context.Context) error {
	workerCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	for i := 0; i < x.numWorkers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			x.runWorker(workerCtx, index)
		}(i)
	}

	x.notifyEvent(ctx, &DispatcherStartEvent{NumWorkers: x.numWorkers})

	x.logger.Printf("%d workers started", x.numWorkers)

	var err error
	for x.haveWorkToDo() {
		if err = x.enqueue(ctx); err != nil {
			break
		}

		if x.runningCount == 0 {
			break
		}

		if err = x.waitResults(ctx); err != nil {
			break
		}
	}

	x.logger.Println("Stopping workers")

	cancel()
	close(x.requestCh)
	wg.Wait()
	close(x.resultCh)
	close(x.eventCh)

	x.logger.Println("All workers stopped")

	// Drain requestCh which contains tasks not yet picked up by worker.
	for task := range x.requestCh {
		task.State = TaskReady
		x.graph.ReadyList.PushFront(task)
		x.runningCount--
	}

	// Drain eventCh.
	for event := range x.eventCh {
		x.notifyEvent(ctx, event)
	}
	// Drain resultCh.
	for task := range x.resultCh {
		x.complete(ctx, task)
	}

	if err == nil && x.haveWorkToDo() {
		err = ErrIncomplete
	}

	x.notifyEvent(ctx, &DispatcherEndEvent{Err: err})

	return err
}

func (x *execution) enqueue(ctx context.Context) error {
	for x.runningCount < x.numWorkers {
		if x.graph.ReadyList.Len() == 0 {
			break
		}
		// Peek a ready task without removing from the ReadyList,
		// because if enqueue failed (due to context cancellation), leave that task in the list.
		elm := x.graph.ReadyList.Front()
		task := elm.Value.(*Task)
		task.State = TaskQueued
		select {
		case <-ctx.Done():
			task.State = TaskReady
			return ctx.Err()
		case x.requestCh <- task:
			x.graph.ReadyList.Remove(elm)
			x.runningCount++
			x.logger.Printf("Enqueued task %s", task.Name())
		}
	}
	return nil
}

func (x *execution) waitResults(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case event := <-x.eventCh:
		x.notifyEvent(ctx, event)
	case task := <-x.resultCh:
		x.complete(ctx, task)
	}
	return nil
}

func (x *execution) complete(ctx context.Context, task *Task) {
	x.graph.Complete(task)
	x.runningCount--
	x.logger.Printf("Completed task %s, err: %v", task.Name(), task.Err)
	x.notifyEvent(ctx, &TaskCompleteEvent{Task: task})
}

func (x *execution) notifyEvent(ctx context.Context, event DispatcherEvent) {
	if handler := x.dispatcher.EventHandler; handler != nil {
		base := event.(dispatcherEventBaseAccessor).eventBase()
		base.dispatcher, base.graph = x.dispatcher, x.graph
		handler.HandleEvent(ctx, event)
	}
}

func (x *execution) runWorker(ctx context.Context, index int) {
	for {
		select {
		case <-ctx.Done():
			return
		case t, ok := <-x.requestCh:
			if !ok {
				return
			}
			x.logger.Printf("Worker %d start task %s", index, t.Name())
			t.StartTime, t.State = time.Now(), TaskRunning
			t.Outputs = nil
			x.eventCh <- &TaskStartEvent{Task: t, Worker: index}
			var result *TaskResult
			result, t.Err = x.executeTask(ctx, t, index)
			t.EndTime, t.State = time.Now(), TaskCompleted
			x.writeTaskResult(t, result)
			x.logger.Printf("Worker %d complete task %s", index, t.Name())
			x.resultCh <- t
		}
	}
}

func (x *execution) executeTask(ctx context.Context, task *Task, worker int) (*TaskResult, error) {
	xctx := ToolExecContext{
		Task:      task,
		Worker:    worker,
		CacheDir:  x.dispatcher.CacheDir,
		OutDir:    filepath.Join(x.dispatcher.OutBaseDir, task.Target.Project.Dir),
		Skippable: !task.Target.Meta().Always && !task.NoSkip,
	}
	result := x.loadTaskResult(task)
	if result.SuccessBuildStartTime == 0 || result.SuccessBuildEndTime == 0 {
		x.logger.Println("NotSkippable: no previous successful build.")
		xctx.Skippable = false
	}
	if xctx.Skippable {
		for dep := range task.DepOn {
			if !dep.Skipped() {
				x.logger.Printf("NotSkippable: dep %s not skipped.", dep.Name())
				xctx.Skippable = false
				break
			}
			depResult := x.loadTaskResult(dep)
			// Not skippable if success build of dep is later than this task.
			if depResult.SuccessBuildStartTime == 0 || depResult.SuccessBuildEndTime == 0 {
				x.logger.Printf("NotSkippable: dep %s has no successful build.", dep.Name())
				xctx.Skippable = false
				break
			}
			if depResult.SuccessBuildStartTime > result.SuccessBuildStartTime ||
				depResult.SuccessBuildEndTime > result.SuccessBuildStartTime {
				x.logger.Printf("NotSkippable: dep %s is newer than current task.", dep.Name())
				xctx.Skippable = false
				break
			}
		}
	}
	tool, ok := task.Target.Tool()
	if !ok {
		var err error
		if tool, err = x.createTool(task.Target); err != nil {
			return result, err
		}
	}
	if tool == nil {
		if xctx.Skippable {
			return result, ErrSkipped
		}
		return result, nil
	}
	xctx.Task.Executor = tool
	os.Remove(x.taskResultFile(task))

	xctx.ExtraEnv = []string{
		fmt.Sprintf("REPOS_PROJECT=%s", xctx.Project().Name),
		fmt.Sprintf("REPOS_TARGET=%s", xctx.Target().Name.GlobalName()),
		fmt.Sprintf("REPOS_TARGET_NAME=%s", xctx.Target().Name.LocalName),
		fmt.Sprintf("REPOS_ROOT_DIR=%s", xctx.Repo().RootDir),
		fmt.Sprintf("REPOS_PROJECT_DIR=%s", xctx.ProjectDir()),
		fmt.Sprintf("REPOS_SOURCE_DIR=%s", xctx.SourceDir()),
		fmt.Sprintf("REPOS_SOURCE_SUBDIR=%s", xctx.SourceSubDir()),
		fmt.Sprintf("REPOS_METAFOLDER=%s", xctx.MetaFolder()),
		fmt.Sprintf("REPOS_PROJECT_META_DIR=%s", xctx.MetaDir()),
		fmt.Sprintf("REPOS_OUTPUT_BASE=%s", xctx.Repo().OutDir()),
		fmt.Sprintf("REPOS_OUTPUT_DIR=%s", xctx.OutDir),
	}
	if xctx.Skippable {
		xctx.ExtraEnv = append(xctx.ExtraEnv, "REPOS_TASK_SKIPPABLE=1")
	}

	if err := os.MkdirAll(xctx.CacheDir, 0755); err != nil {
		return result, fmt.Errorf("create cache dir %q error: %w", xctx.CacheDir, err)
	}
	if err := os.MkdirAll(x.dispatcher.LogDir, 0755); err != nil {
		return result, fmt.Errorf("create log dir %q error: %w", x.dispatcher.LogDir, err)
	}
	if err := os.MkdirAll(xctx.OutDir, 0755); err != nil {
		return result, fmt.Errorf("create out dir %q error: %w", xctx.OutDir, err)
	}

	logFn := filepath.Join(x.dispatcher.LogDir, task.Name()+".log")
	logFile, err := os.Create(logFn)
	if err != nil {
		return result, fmt.Errorf("create log file %q error: %w", logFn, err)
	}
	defer logFile.Close()
	outFn := filepath.Join(x.dispatcher.LogDir, task.Name()+".out")
	outFile, err := os.Create(outFn)
	if err != nil {
		return result, fmt.Errorf("create stdout file %q error: %w", outFn, err)
	}
	defer outFile.Close()
	xctx.LogWriter = logFile
	xctx.Stdout, xctx.Stderr = outFile, outFile
	xctx.Logger = log.New(xctx.LogWriter, task.Target.ToolName()+" ", log.LstdFlags)
	err = tool.Execute(ctx, &xctx)
	if err != nil && err != ErrSkipped {
		return result, err
	}
	if regErr := x.registerToolIfRequested(&xctx); regErr != nil {
		return result, regErr
	}
	return result, err
}

func (x *execution) taskResultFile(task *Task) string {
	return filepath.Join(x.dispatcher.CacheDir, task.Name()+".result")
}

func (x *execution) loadTaskResult(task *Task) *TaskResult {
	fn := x.taskResultFile(task)
	result, err := loadTaskResultFrom(fn)
	if err != nil {
		x.logger.Printf("TaskResult %q: %v", task.Name(), err)
		return &TaskResult{}
	}
	return result
}

func (x *execution) writeTaskResult(task *Task, result *TaskResult) {
	result.StartTime = task.StartTime.UnixNano()
	result.EndTime = task.EndTime.UnixNano()
	result.Skipped = false
	if task.Err == ErrSkipped {
		result.Skipped = true
	} else if task.Err != nil {
		errMsg := task.Err.Error()
		result.Err = &errMsg
	} else {
		result.SuccessBuildStartTime = result.StartTime
		result.SuccessBuildEndTime = result.EndTime
	}
	data, err := json.Marshal(result)
	if err != nil {
		x.logger.Printf("EncodeResult of %q error: %v", task.Name(), err)
		return
	}
	fn := x.taskResultFile(task)
	if err := ioutil.WriteFile(fn, data, 0644); err != nil {
		x.logger.Printf("WriteResult %q error: %v", fn, err)
	}
}

func (x *execution) createTool(target *Target) (ToolExecutor, error) {
	x.dispatcher.toolsLock.RLock()
	tool, ok := x.dispatcher.registeredTools[target.ToolName()]
	x.dispatcher.toolsLock.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown tool: %q", target.ToolName())
	}
	executor, err := tool.CreateToolExecutor(target)
	if err != nil {
		return nil, fmt.Errorf("create tool %q error: %w", target.ToolName(), err)
	}
	return executor, err
}

func (x *execution) registerToolIfRequested(xctx *ToolExecContext) error {
	reg := xctx.Target().toolReg
	if reg == nil {
		return nil
	}
	tool := &ExtTool{Task: xctx.Task, ShellScript: reg.meta.ShellScript}
	if reg.meta.Src != "" {
		tool.Executable = filepath.Join(xctx.SourceDir(), reg.meta.Src)
	} else {
		if xctx.Task.Outputs == nil {
			return fmt.Errorf("register-tool %q no outputs from task", reg.meta.Name)
		}
		out := xctx.Task.Outputs.Primary
		if reg.meta.Out != "" {
			out = xctx.Task.Outputs.Extra[reg.meta.Out]
		}
		if out == "" {
			return fmt.Errorf("register-tool %q output not found", reg.meta.Name)
		}
		tool.Executable = filepath.Join(xctx.OutDir, out)
	}
	envs, err := xctx.RenderEnvs(reg.envTemplates)
	if err != nil {
		return fmt.Errorf("register-tool %q envs: %w", reg.meta.Name, err)
	}
	args, err := xctx.RenderTemplates(reg.argTemplates)
	if err != nil {
		return fmt.Errorf("register-tool %q args: %w", reg.meta.Name, err)
	}
	tool.Envs, tool.Args = envs, args
	x.dispatcher.toolsLock.Lock()
	defer x.dispatcher.toolsLock.Unlock()
	if existing, ok := x.dispatcher.registeredTools[reg.meta.Name]; ok {
		return fmt.Errorf("register-tool %q already registered by %q", reg.meta.Name, existing.Task.Name())
	}
	x.dispatcher.registeredTools[reg.meta.Name] = tool
	xctx.Logger.Printf("Tool %q registered by %q", reg.meta.Name, xctx.Task.Name())
	return nil
}

func loadTaskResultFrom(fn string) (*TaskResult, error) {
	data, err := ioutil.ReadFile(fn)
	if err != nil {
		return nil, fmt.Errorf("load error: %w", err)
	}
	var result TaskResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}
	return &result, nil
}
