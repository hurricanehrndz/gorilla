package report

import "github.com/1dustindavis/gorilla/pkg/catalog"

// FailedItem records an item whose action failed during this run
type FailedItem struct {
	Name    string
	Version string
	Action  string
	Error   string
}

// DeferredItem records an item whose action was deferred (e.g. a blocking app
// was running) during this run
type DeferredItem struct {
	Name    string
	Version string
	Action  string
	Reason  string
}

// NoActionItem records an item whose status check found nothing to do: an
// install or update already present, or an uninstall already absent
type NoActionItem struct {
	Name    string
	Version string
	Action  string
}

// Report holds the state of a single managed run (K7: no package globals)
type Report struct {
	// InstalledItems contains a list of items we successfully installed
	InstalledItems []catalog.Item

	// UninstalledItems contains a list of items we successfully uninstalled
	UninstalledItems []catalog.Item

	// FailedItems contains a list of items whose actions failed
	FailedItems []FailedItem

	// DeferredItems contains a list of items whose actions were deferred
	DeferredItems []DeferredItem

	// NoActionItems contains a list of items whose status check found nothing to do
	NoActionItems []NoActionItem
}

// New returns a fresh Report for one run
func New() *Report {
	return &Report{}
}
