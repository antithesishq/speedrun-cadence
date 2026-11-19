package main

import "testing"

func TestCheckHistoryFlagsDoubleTimerResolution(t *testing.T) {
	evs := []Ev{{ID: 1, Type: "WorkflowExecutionStarted"}, {ID: 2, Type: "TimerStarted", Name: "1"},
		{ID: 3, Type: "TimerCanceled", Ref: 2}, {ID: 4, Type: "TimerFired", Ref: 2}, {ID: 5, Type: "WorkflowExecutionCompleted"}}
	if p := historyProblems(evs, true, false); len(p) == 0 {
		t.Fatal("double timer resolution not flagged")
	}
	ok := []Ev{{ID: 1, Type: "WorkflowExecutionStarted"}, {ID: 2, Type: "TimerStarted"}, {ID: 3, Type: "TimerCanceled", Ref: 2}, {ID: 4, Type: "WorkflowExecutionCompleted"}}
	if p := historyProblems(ok, true, false); len(p) != 0 {
		t.Fatalf("false problem: %v", p)
	}
}
