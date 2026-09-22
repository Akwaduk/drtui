package engine

import (
	"errors"
	"fmt"
)

var ErrCyclicDependency = errors.New("plan contains a cyclic dependency")

// TopologicalOrder returns a stable order. If multiple steps are ready, their
// declaration order in the plan is the tie-breaker.
func TopologicalOrder(steps []Step) ([]string, error) {
	index := make(map[string]int, len(steps))
	indegree := make(map[string]int, len(steps))
	dependents := make(map[string][]string, len(steps))

	for position, step := range steps {
		if step.ID == "" {
			return nil, fmt.Errorf("step at position %d has no id", position)
		}
		if _, exists := index[step.ID]; exists {
			return nil, fmt.Errorf("duplicate step id %q", step.ID)
		}
		index[step.ID] = position
		indegree[step.ID] = len(step.Needs)
	}

	for _, step := range steps {
		seen := make(map[string]struct{}, len(step.Needs))
		for _, dependency := range step.Needs {
			if _, exists := index[dependency]; !exists {
				return nil, fmt.Errorf("step %q needs unknown step %q", step.ID, dependency)
			}
			if _, duplicate := seen[dependency]; duplicate {
				return nil, fmt.Errorf("step %q repeats dependency %q", step.ID, dependency)
			}
			seen[dependency] = struct{}{}
			dependents[dependency] = append(dependents[dependency], step.ID)
		}
	}

	ready := make([]bool, len(steps))
	for _, step := range steps {
		if indegree[step.ID] == 0 {
			ready[index[step.ID]] = true
		}
	}

	order := make([]string, 0, len(steps))
	for len(order) < len(steps) {
		next := -1
		for position, isReady := range ready {
			if isReady {
				next = position
				break
			}
		}
		if next < 0 {
			return nil, ErrCyclicDependency
		}

		ready[next] = false
		stepID := steps[next].ID
		order = append(order, stepID)
		for _, dependent := range dependents[stepID] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				ready[index[dependent]] = true
			}
		}
	}
	return order, nil
}

// RollbackOrder selects successfully completed members of a transaction and
// reverses their topological order. Completion timing never affects rollback.
func RollbackOrder(steps []Step, transaction string, succeeded map[string]bool) ([]string, error) {
	order, err := TopologicalOrder(steps)
	if err != nil {
		return nil, err
	}
	stepByID := make(map[string]Step, len(steps))
	for _, step := range steps {
		stepByID[step.ID] = step
	}

	rollback := make([]string, 0, len(order))
	for position := len(order) - 1; position >= 0; position-- {
		step := stepByID[order[position]]
		if step.Transaction == transaction && succeeded[step.ID] && step.Rollback != nil {
			rollback = append(rollback, step.ID)
		}
	}
	return rollback, nil
}
