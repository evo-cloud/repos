package repos

import (
	"container/list"
	"fmt"
	"time"
)

// TaskGraph is a graph of task for execution.
type TaskGraph struct {
	Repo         *Repo
	Tasks        map[string]*Task
	ReadyList    list.List
	CompleteList list.List
}

// Task wraps a target with states for execution.
type Task struct {
	Graph     *TaskGraph
	Target    *Target
	NoSkip    bool
	DepOn     map[*Task]struct{}
	DepBy     map[*Task]struct{}
	DepDone   map[*Task]struct{}
	State     TaskState
	Executor  ToolExecutor
	StartTime time.Time
	EndTime   time.Time
	Outputs   *OutputFiles
	Err       error
}

// OutputFiles specifies the output files as a result of the target.
// All file path are relative to project's output directory.
type OutputFiles struct {
	// Primary is the primary file.
	Primary string
	// Extra provides additional files indexed by keys.
	Extra map[string]string
	// GeneratedFiles are list of files generated in the source dir
	// of the current task.
	GeneratedFiles []string
}

// TaskState is the state of a task.
type TaskState int

// Values of TaskState
const (
	TaskNotReady TaskState = iota
	TaskReady
	TaskQueued
	TaskRunning
	TaskCompleted
)

// BuildTaskGraph builds a TaskGraph with required target names.
func BuildTaskGraph(r *Repo, requiredTargets ...string) (*TaskGraph, error) {
	g := &TaskGraph{
		Repo:  r,
		Tasks: make(map[string]*Task),
	}
	var resolveList list.List
	for _, name := range requiredTargets {
		tn := SplitTargetName(name)
		if tn.Project == "" {
			return nil, fmt.Errorf("not a global target name: %q", name)
		}
		target := r.FindTarget(tn)
		if target == nil {
			return nil, fmt.Errorf("unknown target %q", tn.GlobalName())
		}
		if task, newTask := g.addTarget(target); newTask {
			resolveList.PushBack(task)
		}
	}
	for resolveList.Len() > 0 {
		elm := resolveList.Front()
		task := elm.Value.(*Task)
		resolveList.Remove(elm)
		for _, name := range task.Target.meta.Deps {
			tn := SplitTargetName(name)
			if tn.Project == "" {
				tn.Project = task.Target.Name.Project
			}
			depTarget := r.FindTarget(tn)
			if depTarget == nil {
				return nil, fmt.Errorf("unknown dependency %q of target %q", name, task.Target.Name.GlobalName())
			}
			depTask, newTask := g.addTarget(depTarget)
			if newTask {
				resolveList.PushBack(depTask)
			}
			task.DepOn[depTask] = struct{}{}
			depTask.DepBy[task] = struct{}{}
		}
	}

	return g, nil
}

// Prepare prepares the graph for execution. It returns a list of ready tasks and tasks with cyclic dependencies.
func (g *TaskGraph) Prepare() map[*Task]struct{} {
	notReady := make(map[*Task]struct{})
	g.ReadyList.Init()
	g.CompleteList.Init()
	var ready list.List
	for _, task := range g.Tasks {
		task.State = TaskNotReady
		task.DepDone = make(map[*Task]struct{})
		task.Err = nil
		if len(task.DepOn) == 0 {
			task.State = TaskReady
			g.ReadyList.PushBack(task)
			ready.PushBack(task)
			continue
		}
		notReady[task] = struct{}{}
	}
	for ready.Len() > 0 {
		elem := ready.Front()
		task := elem.Value.(*Task)
		ready.Remove(elem)
		for depBy := range task.DepBy {
			depBy.DepDone[task] = struct{}{}
			if len(depBy.DepDone) >= len(depBy.DepOn) {
				depBy.DepDone = make(map[*Task]struct{})
				ready.PushBack(depBy)
				delete(notReady, depBy)
			}
		}
	}
	return notReady
}

// Complete marks a task completed and activates other tasks depending on it.
func (g *TaskGraph) Complete(task *Task) {
	task.State = TaskCompleted
	g.CompleteList.PushBack(task)
	if task.Failed() {
		return
	}
	for depBy := range task.DepBy {
		depBy.DepDone[task] = struct{}{}
		if len(depBy.DepDone) >= len(depBy.DepOn) {
			g.ReadyList.PushBack(depBy)
			depBy.State = TaskReady
		}
	}
}

func (g *TaskGraph) addTarget(target *Target) (*Task, bool) {
	name := target.Name.GlobalName()
	task := g.Tasks[name]
	if task != nil {
		return task, false
	}
	task = &Task{
		Graph:  g,
		Target: target,
		DepOn:  make(map[*Task]struct{}),
		DepBy:  make(map[*Task]struct{}),
	}
	g.Tasks[name] = task
	return task, true
}

// Name returns the global name of the target.
func (t *Task) Name() string {
	return t.Target.Name.GlobalName()
}

// Failed indicates the task failed.
func (t *Task) Failed() bool {
	return t.Err != nil && t.Err != ErrSkipped
}

// Skipped indicates the task is skipped.
func (t *Task) Skipped() bool {
	return t.Err == ErrSkipped
}
