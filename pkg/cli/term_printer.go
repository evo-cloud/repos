package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"repos/pkg/repos"
)

// TermPrinter provides an output-only UserInterface for ANSI terminal.
type TermPrinter struct {
}

// TaskEventHandler implements UserInterface.
func (p *TermPrinter) TaskEventHandler(options EventHandlingOptions) repos.EventHandler {
	return newTasksPrinter(os.Stdout, options.LogReader)
}

// PrintProjectList prints project list.
func (p *TermPrinter) PrintProjectList(projects []*repos.Project) {
	for _, project := range projects {
		fmt.Printf("\x1b[36;1m%s\x1b[m \x1b[37m[%s]\x1b[m\n", project.Name, project.Dir)
		if desc := project.Meta().Description; desc != "" {
			lines := strings.Split(desc, "\n")
			for _, line := range lines {
				fmt.Printf("  \x1b[37;0m%s\x1b[m\n", line)
			}
		}
	}
}

// PrintTargetList prints target list.
func (p *TermPrinter) PrintTargetList(targets []*repos.Target) {
	for _, target := range targets {
		fmt.Printf("\x1b[36;1m%s\x1b[m\n", target.Name.GlobalName())
		if desc := target.Meta().Description; desc != "" {
			fmt.Printf("  \x1b[37;0m%s\x1b[m\n", desc)
		}
	}
}

// PrintLog prints log from reader.
func (p *TermPrinter) PrintLog(reader io.Reader) {
	io.Copy(os.Stdout, reader)
}

// PrintTaskStatus prints task status.
func (p *TermPrinter) PrintTaskStatus(name string, result *repos.TaskResult, outputs *repos.OutputFiles) {
	resultStr := " \x1b[33;1m??\x1b[m"
	var durStr string
	if result != nil {
		if result.Skipped {
			resultStr = " \x1b[37;1mSKIP\x1b[m"
		} else {
			dur := time.Unix(0, result.EndTime).Sub(time.Unix(0, result.StartTime)).Truncate(time.Millisecond)
			durStr = fmt.Sprintf(" \x1b[35;1m%s\x1b[m", dur)
			if result.Err != nil {
				resultStr = " \x1b[31;1mFAIL\x1b[m"
			} else {
				resultStr = " \x1b[32;1mOK\x1b[m"
			}
		}
	}
	fmt.Printf("\x1b[36;1m%s\x1b[m%s%s\n", name, resultStr, durStr)

	if result != nil {
		if result.SuccessBuildStartTime != 0 && result.SuccessBuildEndTime != 0 {
			fmt.Println("Last successful build:")
			fmt.Printf("  StartAt: %s\n", time.Unix(0, result.SuccessBuildStartTime).Format(time.StampMilli))
			fmt.Printf("  EndAt:   %s\n", time.Unix(0, result.SuccessBuildEndTime).Format(time.StampMilli))
		}
		fmt.Println("Last build:")
		fmt.Printf("  StartAt: %s\n", time.Unix(0, result.StartTime).Format(time.StampMilli))
		fmt.Printf("  EndAt:   %s\n", time.Unix(0, result.EndTime).Format(time.StampMilli))
		if !result.Skipped && result.Err != nil {
			fmt.Printf("  \x1b[31;1mError:\x1b[m \x1b[31m%s\x1b[m\n", *result.Err)
		}
	}

	if outputs == nil {
		return
	}
	fmt.Println("Outputs:")
	if outputs.Primary != "" {
		fmt.Printf("  Primary: \x1b[32;1m%s\x1b[m\n", outputs.Primary)
	}
	if len(outputs.Extra) > 0 {
		fmt.Printf("  Extra:\n")
		for key, val := range outputs.Extra {
			fmt.Printf("    \x1b[34m%s\x1b[m: %s\n", key, val)
		}
	}
	if len(outputs.GeneratedFiles) > 0 {
		fmt.Printf("  Generated:\n")
		for _, fn := range outputs.GeneratedFiles {
			fmt.Printf("    \x1b[33m%s\x1b[m\n", fn)
		}
	}
}

// PrintError implements UserInterface.
func (p *TermPrinter) PrintError(err error) {
	fmt.Fprintf(os.Stderr, "\x1b[31;1mError:\x1b[m \x1b[31m%v.\x1b[m\n", err)
}

type tasksPrinter struct {
	succeeded   int
	skipped     int
	failed      int
	logReader   TaskLogReader
	writer      io.Writer
	tasks       map[*repos.Task]int
	currentRows int
}

func newTasksPrinter(w io.Writer, logReader TaskLogReader) *tasksPrinter {
	p := &tasksPrinter{
		writer:    w,
		logReader: logReader,
		tasks:     make(map[*repos.Task]int),
	}
	return p
}

func (p *tasksPrinter) HandleEvent(ctx context.Context, event repos.DispatcherEvent) {
	total := len(event.Graph().Tasks)
	completed := event.Graph().CompleteList.Len()
	percentage := float32(completed) * 100 / float32(total)
	switch ev := event.(type) {
	case *repos.DispatcherStartEvent:
		p.succeeded = 0
		p.skipped = 0
		p.failed = 0
	case *repos.DispatcherEndEvent:
		p.complete(p.succeeded, p.skipped, p.failed, total-completed)
	case *repos.TaskStartEvent:
		p.taskStart(ev.Task, ev.Worker, percentage)
	case *repos.TaskCompleteEvent:
		switch {
		case ev.Task.Failed():
			p.failed++
		case ev.Task.Skipped():
			p.skipped++
		default:
			p.succeeded++
		}
		p.taskComplete(ev.Task, percentage)
	}
}

func (p *tasksPrinter) taskStart(task *repos.Task, worker int, percentage float32) {
	p.tasks[task] = worker
	p.moveToStart()
	p.renderRows(percentageState(percentage))
}

func (p *tasksPrinter) taskComplete(task *repos.Task, percentage float32) {
	delete(p.tasks, task)
	var linePrefix, dur string
	switch {
	case task.Failed():
		linePrefix = "\x1b[31;1m:("
	case task.Skipped():
		linePrefix = "\x1b[36;1m:]"
	default:
		linePrefix = "\x1b[32;1m:)"
	}
	if !task.Skipped() {
		dur = fmt.Sprintf(" \x1b[35;1m%s\x1b[m", task.EndTime.Sub(task.StartTime).Truncate(time.Millisecond))
	}
	p.moveToStart()
	p.printf("\x1b[2K\r%s\x1b[m \x1b[37m%s\x1b[m%s\n", linePrefix, task.Name(), dur)
	for i := 1; i < p.currentRows; i++ {
		p.printf("\x1b[2K\n")
	}
	if p.currentRows > 1 {
		p.printf("\x1b[%dA", p.currentRows-1)
	}
	p.currentRows = 0
	if task.Failed() {
		p.printf("    \x1b[31m%v\x1b[m\n", task.Err)
		p.printTaskLog(task)
	}
	p.renderRows(percentageState(percentage))
}

func (p *tasksPrinter) complete(succeeded, skipped, failed, incomplete int) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "\x1b[32mOK\x1b[m \x1b[32;1m%d\x1b[m", succeeded)
	if skipped != 0 {
		fmt.Fprintf(&buf, " \x1b[36mSkipped\x1b[m \x1b[36;1m%d\x1b[m", skipped)
	}
	if failed != 0 {
		fmt.Fprintf(&buf, " \x1b[31mFailed\x1b[m \x1b[31;1m%d\x1b[m", failed)
	}
	if incomplete != 0 {
		fmt.Fprintf(&buf, " \x1b[37;0mNotRun\x1b[m \x1b[37m%d\x1b[m", incomplete)
	}
	p.tasks = nil
	p.moveToStart()
	p.renderRows(buf.String())
	p.printf("\n")
}

func (p *tasksPrinter) moveToStart() {
	// Cursor is always placed at the next row of the last row.
	// Move to beginning.
	p.printf("\x1b[2K\r")
	if p.currentRows > 0 {
		p.printf("\x1b[%dA", p.currentRows)
	}
}

func (p *tasksPrinter) renderRows(state string) {
	workers := make(map[int]*repos.Task)
	for t, w := range p.tasks {
		workers[w] = t
	}
	slots := make([]int, 0, len(workers)+1)
	for n := range workers {
		slots = append(slots, n)
	}
	sort.Ints(slots)
	for _, w := range slots {
		p.printf("\x1b[2K\r\x1b[5m\x1b[32m>>\x1b[m \x1b[36m%2d\x1b[m \x1b[37m%s\x1b[m\n", w, workers[w].Name())
	}
	for i := len(slots); i < p.currentRows; i++ {
		p.printf("\x1b[2K\n")
	}
	if p.currentRows > len(slots) {
		p.printf("\x1b[%dA", p.currentRows-len(slots))
	}
	p.currentRows = len(slots)
	p.printf("\x1b[2K\r%s", state)
}

func (p *tasksPrinter) printf(format string, args ...interface{}) {
	fmt.Fprintf(p.writer, format, args...)
}

func (p *tasksPrinter) printTaskLog(task *repos.Task) {
	if p.logReader == nil {
		return
	}
	reader, err := p.logReader(task)
	if err != nil {
		p.printf("    \x1b[31mFailed to open log: %v.\x1b[m\n", err)
		p.printf("    \x1b[31mPlease use \x1b[37mlog %s\x1b[31m command to inspect the output.\x1b[m\n", task.Name())
		return
	}
	defer reader.Close()
	io.Copy(p.writer, reader)
	p.printf("\n")
}

func percentageState(percentage float32) string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%.1f%% [", percentage)
	blocks := int(percentage * 20 / 100)
	for i := 0; i < blocks; i++ {
		fmt.Fprintf(&buf, "=")
	}
	for i := blocks; i < 20; i++ {
		fmt.Fprintf(&buf, " ")
	}
	fmt.Fprintf(&buf, "]")
	return buf.String()
}
