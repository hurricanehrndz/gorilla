package report

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"github.com/1dustindavis/gorilla/pkg/catalog"
)

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
	// Items contains the data we will save to GorillaReport
	Items map[string]any

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
	return &Report{Items: make(map[string]any)}
}

// fakeTime is used to override currentTime when running tests
var fakeTime time.Time

// Start adds the data we already know at the beginning of a run
func (r *Report) Start() {
	// Get the current time
	currentTime := time.Now().UTC()

	// If fakeTime is not zero, we should use it instead
	if !fakeTime.IsZero() {
		currentTime = fakeTime
	}

	// Add the end time to our map
	r.Items["StartTime"] = fmt.Sprint(currentTime.Format("2006-01-02 15:04:05 -0700"))

	// Store the current user
	currentUser, userErr := user.Current()
	if userErr != nil {
		fmt.Println("Unable to determine current user", userErr)
	}
	r.Items["CurrentUser"] = fmt.Sprint(currentUser.Username)

	// Store the hostname
	hostName, hostErr := os.Hostname()
	if hostErr != nil {
		fmt.Println("Unable to determine current time", hostErr)
	}
	r.Items["HostName"] = fmt.Sprint(hostName)
}

// compile folds the run results into the Items map
func (r *Report) compile() {
	r.Items["InstalledItems"] = r.InstalledItems
	r.Items["UninstalledItems"] = r.UninstalledItems
	r.Items["FailedItems"] = r.FailedItems
	r.Items["DeferredItems"] = r.DeferredItems
}

// End will compile everything and save to disk
func (r *Report) End() {
	// Compile everything
	r.compile()

	// Get the current time
	currentTime := time.Now().UTC()

	// If fakeTime is not zero, we should use it instead
	if !fakeTime.IsZero() {
		currentTime = fakeTime
	}

	// Add the end time to our map
	r.Items["EndTime"] = fmt.Sprint(currentTime.Format("2006-01-02 15:04:05 -0700"))

	// Convert it all to json
	reportJSON, marshalErr := json.Marshal(r.Items)
	if marshalErr != nil {
		fmt.Println("Unable to create GorillaReport json", marshalErr)
	}

	// Write Items to disk as GorillaReport.json
	reportPath := filepath.Join(os.Getenv("ProgramData"), "gorilla/GorillaReport.json")
	writeErr := os.WriteFile(reportPath, reportJSON, 0o644)
	if writeErr != nil {
		fmt.Println("Unable to write GorillaReport.json to disk:", writeErr)
	}
}

// Print writes the report to stdout instead of writing to disk
// Used in check only mode
func (r *Report) Print() {
	// Compile everything
	r.compile()

	reportJSON, marshalErr := json.MarshalIndent(r.Items, "", "    ")
	fmt.Println(string(reportJSON))
	if marshalErr != nil {
		fmt.Println("Unable to create GorillaReport json", marshalErr)
	}
}
