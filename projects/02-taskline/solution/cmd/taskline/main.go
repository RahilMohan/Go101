// Command taskline is a TODO manager with JSON persistence.
//
// Usage:
//
//	taskline add <title...>   add a task (remaining words form the title)
//	taskline list             print tasks as ID, STATUS, TITLE columns
//	taskline do <id>          mark a task done
//	taskline start <id>       mark a task in progress
//	taskline rm <id>          remove a task
//
// The store file is taken from TASKLINE_FILE, defaulting to ~/.taskline.json.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	solution "go101.dev/projects/02-taskline/solution"
)

const usage = `usage: taskline <command> [args]

commands:
  add <title...>   add a task
  list             list tasks (ID, STATUS, TITLE)
  do <id>          mark a task done
  start <id>       mark a task in progress
  rm <id>          remove a task

The store file is TASKLINE_FILE or ~/.taskline.json.`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}

	store, err := solution.OpenStore(storePath())
	if err != nil {
		fatal(err)
	}

	switch os.Args[1] {
	case "add":
		title := strings.Join(os.Args[2:], " ")
		if title == "" {
			fmt.Fprintln(os.Stderr, "taskline: add requires a title")
			os.Exit(2)
		}
		task, err := store.Add(title)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("added #%d %s\n", task.ID, task.Title)
	case "list":
		tasks, err := store.List()
		if err != nil {
			fatal(err)
		}
		sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
		for _, t := range tasks {
			fmt.Printf("%4d  %-5s  %s\n", t.ID, t.Status, t.Title)
		}
	case "do", "start", "rm":
		id, err := strconv.Atoi(arg(os.Args))
		if err != nil {
			fmt.Fprintf(os.Stderr, "taskline: %s requires an integer id\n", os.Args[1])
			os.Exit(2)
		}
		switch os.Args[1] {
		case "do":
			err = store.SetStatus(id, solution.StatusDone)
		case "start":
			err = store.SetStatus(id, solution.StatusDoing)
		case "rm":
			err = store.Remove(id)
		}
		if err != nil {
			if errors.Is(err, solution.ErrNotFound) {
				fmt.Fprintf(os.Stderr, "taskline: no task with id %d\n", id)
				os.Exit(1)
			}
			fatal(err)
		}
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
}

func arg(args []string) string {
	if len(args) < 3 {
		return ""
	}
	return args[2]
}

// storePath returns the store file location: TASKLINE_FILE if set, else
// ~/.taskline.json.
func storePath() string {
	if p := os.Getenv("TASKLINE_FILE"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fatal(fmt.Errorf("taskline: resolve home directory: %w", err))
	}
	return filepath.Join(home, ".taskline.json")
}

// fatal reports err to stderr and exits 1. Library errors already carry the
// "taskline: " prefix; non-library call sites must add it themselves.
func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
