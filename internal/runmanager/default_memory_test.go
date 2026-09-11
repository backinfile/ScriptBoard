package runmanager

import (
	"sync"
	"testing"
)

func TestDefaultMemoryConcurrentUpdates(t *testing.T) {
	m := &Manager{}
	m.SetDefaultMemory("512MiB")
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for j := 0; j < 1000; j++ {
				m.SetDefaultMemory("768MiB")
				value := m.DefaultMemory()
				if value != "512MiB" && value != "768MiB" {
					t.Errorf("invalid default %q", value)
					return
				}
			}
		}()
	}
	group.Wait()
}
