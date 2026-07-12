package report

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"time"
)

// FailedItem records an item whose action failed during this run
type FailedItem struct {
	Name    string
	Version string
	Action  string
	Error   string
}

var (
	// Items contains the data we will save to GorillaReport
	Items = make(map[string]interface{})

	// InstalledItems contains a list of items we successfully installed
	InstalledItems []interface{}

	// UninstalledItems contains a list of items we successfully uninstalled
	UninstalledItems []interface{}

	// FailedItems contains a list of items whose actions failed
	FailedItems []FailedItem

	// fakeTime is used to override currentTime when running tests
	fakeTime time.Time
)

// Start adds the data we already know at the beginning of a run
func Start() {
	// Get the current time
	currentTime := time.Now().UTC()

	// If fakeTime is not zero, we should use it instead
	if !fakeTime.IsZero() {
		currentTime = fakeTime
	}

	// Add the end time to our map
	Items["StartTime"] = fmt.Sprint(currentTime.Format("2006-01-02 15:04:05 -0700"))

	// Store the current user
	currentUser, userErr := user.Current()
	if userErr != nil {
		fmt.Println("Unable to determine current user", userErr)
	}
	Items["CurrentUser"] = fmt.Sprint(currentUser.Username)

	// Store the hostname
	hostName, hostErr := os.Hostname()
	if hostErr != nil {
		fmt.Println("Unable to determine current time", hostErr)
	}
	Items["HostName"] = fmt.Sprint(hostName)
}

// End will compile everything and save to disk
func End() {
	// Compile everything
	Items["InstalledItems"] = InstalledItems
	Items["UninstalledItems"] = UninstalledItems
	Items["FailedItems"] = FailedItems

	// Get the current time
	currentTime := time.Now().UTC()

	// If fakeTime is not zero, we should use it instead
	if !fakeTime.IsZero() {
		currentTime = fakeTime
	}

	// Add the end time to our map
	Items["EndTime"] = fmt.Sprint(currentTime.Format("2006-01-02 15:04:05 -0700"))

	// Convert it all to json
	reportJSON, marshalErr := json.Marshal(Items)
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
func Print() {
	// Compile everything
	Items["InstalledItems"] = InstalledItems
	Items["UninstalledItems"] = UninstalledItems
	Items["FailedItems"] = FailedItems

	reportJSON, marshalErr := json.MarshalIndent(Items, "", "    ")
	fmt.Println(string(reportJSON))
	if marshalErr != nil {
		fmt.Println("Unable to create GorillaReport json", marshalErr)
	}
}
