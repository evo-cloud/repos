package repos

import "errors"

var (
	// ErrSkipped is used as the return value of ToolExecutor.Execute
	// to indicate the task is skipped.
	ErrSkipped = errors.New("skipped")

	// ErrIncomplete indicates not all tasks are completed.
	ErrIncomplete = errors.New("incomplete")
	// ErrTooManyTools indicates more than one tool is specified in target.rule.
	ErrTooManyTools = errors.New("only one tool can be specified in rule")

	// ErrNoCurrentProject indicates current project is not avaiable.
	ErrNoCurrentProject = errors.New("no current project, please start from inside (or a subdirectory) a project folder")
	// ErrAmbiguousMatch indicates more than one names are matched.
	ErrAmbiguousMatch = errors.New("ambiguous match")
)
