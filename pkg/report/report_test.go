package report

import (
	"testing"

	"github.com/1dustindavis/gorilla/pkg/catalog"
)

// TestNewReportsShareNothing validates that two sequential runs with fresh
// reports do not leak items across runs (K7)
func TestNewReportsShareNothing(t *testing.T) {
	run1 := New()
	run1.InstalledItems = append(run1.InstalledItems, catalog.Item{DisplayName: "run1 item"})
	run1.FailedItems = append(run1.FailedItems, FailedItem{Name: "run1 failure"})
	run1.NoActionItems = append(run1.NoActionItems, NoActionItem{Name: "run1 current"})

	run2 := New()
	if len(run2.InstalledItems) != 0 || len(run2.UninstalledItems) != 0 || len(run2.FailedItems) != 0 || len(run2.NoActionItems) != 0 {
		t.Errorf("fresh report contains items from a previous run: %#v", run2)
	}
}
