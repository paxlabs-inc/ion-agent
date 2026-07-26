package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

var (
	taskHeader = regexp.MustCompile(`^\[task\.([0-9]+(?:\.[0-9]+)*)\]$`)
	quotedID   = regexp.MustCompile(`"([0-9]+(?:\.[0-9]+)*)"`)
)

type task struct {
	id       string
	status   string
	requires []string
	line     int
}

func main() {
	path := "spec/spec.kvx"
	if len(os.Args) == 2 {
		path = os.Args[1]
	}
	file, err := os.Open(path)
	if err != nil {
		fatal(err)
	}
	defer file.Close()
	tasks := map[string]*task{}
	var current *task
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if match := taskHeader.FindStringSubmatch(text); match != nil {
			current = &task{id: match[1], line: line}
			tasks[current.id] = current
			continue
		}
		if strings.HasPrefix(text, "[") {
			current = nil
			continue
		}
		if current == nil {
			continue
		}
		key, value, ok := strings.Cut(text, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "status":
			current.status = strings.Trim(strings.TrimSpace(value), `"`)
		case "requires":
			for _, match := range quotedID.FindAllStringSubmatch(value, -1) {
				current.requires = append(current.requires, match[1])
			}
		}
	}
	if err := scanner.Err(); err != nil {
		fatal(err)
	}
	var failures []string
	for _, item := range tasks {
		if item.status != "done" {
			continue
		}
		for _, required := range item.requires {
			dependency := tasks[required]
			if dependency == nil || dependency.status != "done" {
				failures = append(failures, fmt.Sprintf(
					"task.%s line %d is done but requires task.%s (%s)",
					item.id, item.line, required, status(dependency),
				))
			}
		}
		prefix := item.id + "."
		for _, child := range tasks {
			if directChild(prefix, child.id) && child.status != "done" {
				failures = append(failures, fmt.Sprintf(
					"task.%s line %d is done but child task.%s is %s",
					item.id, item.line, child.id, child.status,
				))
			}
		}
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		for _, failure := range failures {
			fmt.Fprintln(os.Stderr, failure)
		}
		os.Exit(1)
	}
	fmt.Printf("spec dependency/status coherence valid (%d tasks)\n", len(tasks))
}

func directChild(prefix, id string) bool {
	if !strings.HasPrefix(id, prefix) {
		return false
	}
	return !strings.Contains(strings.TrimPrefix(id, prefix), ".")
}

func status(item *task) string {
	if item == nil {
		return "missing"
	}
	return item.status
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
