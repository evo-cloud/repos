package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"repos/pkg/repos"
)

// TextPrinter provides an output-only UserInterface in plain text.
type TextPrinter struct {
}

// TaskEventHandler implements UserInterface.
func (p *TextPrinter) TaskEventHandler(options EventHandlingOptions) repos.EventHandler {
	return &textEventPrinter{logReader: options.LogReader}
}

// PrintProjectList prints project list.
func (p *TextPrinter) PrintProjectList(projects []*repos.Project) {
	for _, project := range projects {
		fmt.Printf("%s %s\n", project.Name, project.Dir)
	}
}

// PrintTargetList prints target list.
func (p *TextPrinter) PrintTargetList(targets []*repos.Target) {
	for _, target := range targets {
		fmt.Println(target.Name.GlobalName())
	}
}

// PrintLog prints log from reader.
func (p *TextPrinter) PrintLog(reader io.Reader) {
	io.Copy(os.Stdout, reader)
}

// PrintTaskStatus prints task status.
func (p *TextPrinter) PrintTaskStatus(name string, result *repos.TaskResult, outputs *repos.OutputFiles) {
	fmt.Printf("Task: %s\n", name)
	if result == nil {
		fmt.Printf("  Result: Unknown\n")
	} else {
		if result.SuccessBuildStartTime != 0 && result.SuccessBuildEndTime != 0 {
			fmt.Println("Last successful build:")
			fmt.Printf("  StartAt: %s\n", time.Unix(0, result.SuccessBuildStartTime))
			fmt.Printf("  EndAt:   %s\n", time.Unix(0, result.SuccessBuildEndTime))
		}
		fmt.Println("Last build:")
		fmt.Printf("  StartAt: %s\n", time.Unix(0, result.StartTime))
		fmt.Printf("  EndAt: %s\n", time.Unix(0, result.EndTime))
		switch {
		case result.Skipped:
			fmt.Printf("  Result: Skipped\n")
		case result.Err == nil:
			fmt.Printf("  Result: Succeeded\n")
		default:
			fmt.Printf("  Result: Failed\n")
			fmt.Printf("  Error: %s\n", *result.Err)
		}
	}

	if outputs == nil {
		return
	}
	fmt.Println("Outputs:")
	fmt.Printf("  Primary: %s\n", outputs.Primary)
	if len(outputs.Extra) > 0 {
		fmt.Printf("  Extra:\n")
		for key, val := range outputs.Extra {
			fmt.Printf("    %s: %s\n", key, val)
		}
	}
	if len(outputs.GeneratedFiles) > 0 {
		fmt.Printf("  Generated:\n")
		for _, fn := range outputs.GeneratedFiles {
			fmt.Printf("    %s\n", fn)
		}
	}
}

// PrintError implements UserInterface.
func (p *TextPrinter) PrintError(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v.\n", err)
}

type textEventPrinter struct {
	succeeded int
	skipped   int
	failed    int
	logReader TaskLogReader
}

func (p *textEventPrinter) HandleEvent(ctx context.Context, event repos.DispatcherEvent) {
	total := len(event.Graph().Tasks)
	completed := event.Graph().CompleteList.Len()
	percentage := fmt.Sprintf("%.1f%%", float32(completed)*100/float32(total))
	switch ev := event.(type) {
	case *repos.DispatcherStartEvent:
		p.succeeded = 0
		p.skipped = 0
		p.failed = 0
		fmt.Printf("BUILD START workers=%d tasks=%d\n", ev.NumWorkers, total)
	case *repos.DispatcherEndEvent:
		fmt.Printf("BUILD END succeeded=%d skipped=%d failed=%d\n", p.succeeded, p.skipped, p.failed)
	case *repos.TaskStartEvent:
		fmt.Printf("%s START %s worker=%d\n", percentage, ev.Task.Name(), ev.Worker)
	case *repos.TaskCompleteEvent:
		if ev.Task.Failed() {
			p.failed++
			fmt.Printf("%s FAILED %s: %v\n", percentage, ev.Task.Name(), ev.Task.Err)
			return
		}
		if ev.Task.Skipped() {
			p.skipped++
			fmt.Printf("%s SKIPPED %s\n", percentage, ev.Task.Name())
			return
		}
		p.succeeded++
		fmt.Printf("%s DONE %s\n", percentage, ev.Task.Name())
	}
}
