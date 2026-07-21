package report

import (
	"bytes"
	"fmt"
	"os"
	"os/user"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/1dustindavis/gorilla/pkg/catalog"
)

// TestStart validates that a properly formated `Items` map
// is created with the expected starting data
func TestStart(t *testing.T) {
	// Set our expectations
	fakeTime = time.Now().UTC()
	expectedTime := fakeTime.Format("2006-01-02 15:04:05 -0700")

	expectedUser, userErr := user.Current()
	if userErr != nil {
		fmt.Println("Unable to determine expected user", userErr)
	}

	expectedHostname, hostErr := os.Hostname()
	if hostErr != nil {
		fmt.Println("Unable to determine current time", hostErr)
	}

	// Put our expectations in a map for comparison
	expectedItems := map[string]any{
		"StartTime":   fmt.Sprint(expectedTime),
		"CurrentUser": fmt.Sprint(expectedUser.Username),
		"HostName":    fmt.Sprint(expectedHostname),
	}

	// Run the `Start` method on a fresh report
	r := New()
	r.Start()

	// Compare the actual struct of items to what we expected
	if !reflect.DeepEqual(expectedItems, r.Items) {
		t.Errorf("\n\nExpected:\n\n%#v\n\nReceived:\n\n %#v", expectedItems, r.Items)
	}
}

// TestEnd validates that a properly formated `Items` map
// is updated with the correct items, including FailedItems
func TestEnd(t *testing.T) {
	// Set our expectations
	fakeTime = time.Now().UTC()
	expectedTime := fakeTime.Format("2006-01-02 15:04:05 -0700")
	expectedInstalls := []catalog.Item{{DisplayName: "test Installs 1"}, {DisplayName: "test Installs 2"}}
	expectedUninstalls := []catalog.Item{{DisplayName: "test Uninstalls 1"}, {DisplayName: "test Uninstalls 2"}}
	expectedFailures := []FailedItem{{Name: "test Failed 1", Version: "1.2.3", Action: "install", Error: "exit status 1"}}
	expectedDeferrals := []DeferredItem{{Name: "test Deferred 1", Version: "1.2.3", Action: "install", Reason: "blocking application(s) running: notepad"}}

	// Apend everything to the correct lists
	r := New()
	r.InstalledItems = append(r.InstalledItems, expectedInstalls...)
	r.UninstalledItems = append(r.UninstalledItems, expectedUninstalls...)
	r.FailedItems = append(r.FailedItems, expectedFailures...)
	r.DeferredItems = append(r.DeferredItems, expectedDeferrals...)

	// Build the expected map for comparison
	expectedItems := map[string]any{
		"EndTime":          fmt.Sprint(expectedTime),
		"InstalledItems":   expectedInstalls,
		"UninstalledItems": expectedUninstalls,
		"FailedItems":      expectedFailures,
		"DeferredItems":    expectedDeferrals,
	}

	// Run the `End` method
	r.End()

	// Compare the actual results
	if !reflect.DeepEqual(expectedItems, r.Items) {
		t.Errorf("\n\nExpected:\n\n%#v\n\nReceived:\n\n %#v", expectedItems, r.Items)
	}
}

// TestPrintEmitsFailedItems validates that check-only output includes the
// failed items of the run
func TestPrintEmitsFailedItems(t *testing.T) {
	r := New()
	r.FailedItems = append(r.FailedItems, FailedItem{Name: "test Failed 1", Version: "1.2.3", Action: "install", Error: "exit status 1"})

	// Capture stdout while printing
	origStdout := os.Stdout
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe creation failed: %v", err)
	}
	os.Stdout = pw
	r.Print()
	os.Stdout = origStdout
	if err := pw.Close(); err != nil {
		t.Fatalf("stdout pipe close failed: %v", err)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(pr); err != nil {
		t.Fatalf("stdout pipe read failed: %v", err)
	}

	out := buf.String()
	for _, want := range []string{`"FailedItems"`, `"test Failed 1"`, `"exit status 1"`} {
		if !strings.Contains(out, want) {
			t.Errorf("Print output missing %q:\n%s", want, out)
		}
	}
}

// TestNewReportsShareNothing validates that two sequential runs with fresh
// reports do not leak items across runs (K7)
func TestNewReportsShareNothing(t *testing.T) {
	run1 := New()
	run1.InstalledItems = append(run1.InstalledItems, catalog.Item{DisplayName: "run1 item"})
	run1.FailedItems = append(run1.FailedItems, FailedItem{Name: "run1 failure"})
	run1.Items["Manifest"] = "run1-manifest"

	run2 := New()
	if len(run2.InstalledItems) != 0 || len(run2.UninstalledItems) != 0 || len(run2.FailedItems) != 0 {
		t.Errorf("fresh report contains items from a previous run: %#v", run2)
	}
	if len(run2.Items) != 0 {
		t.Errorf("fresh report contains map data from a previous run: %#v", run2.Items)
	}
}
