package subtitlederived

import (
	"sync"
	"testing"
)

func TestOperationSubmissionPreservesCheckpointsAndBindsInputs(t *testing.T) {
	service, seed := recoveryTask(t)
	sources := []Source{seed.Items[0].Source}
	id := "00000000-0000-0000-0000-000000000001"
	task, err := service.CreateOperation(sources, []string{"brief"}, "Course", id)
	if err != nil {
		t.Fatal(err)
	}
	task.Items[0].GenerationState = "dispatched"
	if err := service.save(task); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			// Each API invocation creates a new Service; deduplication must still
			// share the operation identity rather than reset the domain task.
			result, err := New(service.Root, service.Library).CreateOperation(sources, []string{"brief"}, "Course", id)
			if err != nil || result.ID != id || result.Items[0].GenerationState != "dispatched" {
				t.Errorf("checkpoint replaced: %+v %v", result, err)
			}
		}()
	}
	workers.Wait()
	if _, err := service.CreateOperation(sources, []string{"detailed"}, "Course", id); err == nil {
		t.Fatal("operation rebound to changed mode")
	}
	if _, err := service.CreateOperation(sources, []string{"brief"}, "Other course", id); err == nil {
		t.Fatal("operation rebound to changed course")
	}
	changed := sources[0]
	changed.Title = "Changed"
	if _, err := service.CreateOperation([]Source{changed}, []string{"brief"}, "Course", id); err == nil {
		t.Fatal("operation rebound to changed source")
	}
}
