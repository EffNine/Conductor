package automode

import (
	"strings"
)

// TaskType is a coarse classification of an incoming request.
type TaskType string

const (
	TaskElite     TaskType = "elite"     // complex agentic coding / reasoning
	TaskCoding    TaskType = "coding"    // code generation, debugging, refactoring
	TaskReasoning TaskType = "reasoning" // analysis, comparison, multi-step logic
	TaskVision    TaskType = "vision"    // image / screenshot understanding
	TaskFast      TaskType = "fast"      // short, simple, low-latency requests
	TaskDefault   TaskType = "default"   // no strong signal
)

// ClassifyTask returns a task profile name based on the request text.
// It uses simple keyword heuristics; callers may override via config later.
func ClassifyTask(text string) string {
	lower := strings.ToLower(text)

	// Vision is the most specific signal.
	if matchesAny(lower, []string{
		"image", "picture", "screenshot", "vision", "look at", "describe this",
		"what is in", "what's in", "diagram", "chart", "photo",
	}) {
		return string(TaskVision)
	}

	// Elite / complex agentic coding: large refactor, architecture, multi-file.
	if matchesAny(lower, []string{
		"implement", "refactor", "architect", "design a system", "build a",
		"create a full", "end-to-end", "multi-step", "complex",
	}) && matchesAny(lower, []string{
		"code", "function", "api", "service", "module", "app", "application",
		"system", "distributed", "microservice", "backend", "infrastructure",
	}) {
		return string(TaskElite)
	}

	// Coding tasks.
	if matchesAny(lower, []string{
		"code", "coding", "program", "function", "debug", "fix", "refactor",
		"implementation", "script", "algorithm", "test case", "unit test",
		"pull request", "commit", "git", "repo", "repository", "syntax",
		"compile", "build error", "runtime error", "stack trace", "exception",
	}) {
		return string(TaskCoding)
	}

	// Reasoning / analysis.
	if matchesAny(lower, []string{
		"analyze", "compare", "evaluate", "explain", "reason", "why", "how does",
		"trade-off", "tradeoff", "pros and cons", "advantages", "disadvantages",
		"summarize and", "step by step", "prove", "derive", "solve",
	}) {
		return string(TaskReasoning)
	}

	// Fast / trivial tasks.
	if matchesAny(lower, []string{
		"hi", "hello", "hey", "quick", "short", "brief", "one sentence",
		"one word", "simple", "just", "only", "greeting", "thank", "thanks",
	}) {
		return string(TaskFast)
	}

	return string(TaskDefault)
}

func matchesAny(text string, terms []string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}
