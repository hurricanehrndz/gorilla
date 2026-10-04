package installer

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/1dustindavis/gorilla/pkg/catalog"
)

// cancelTestItem needs action, and its installer is already in the testdata
// cache, so nothing is downloaded unless a test points it elsewhere.
func cancelTestItem() catalog.Item {
	item := msiItem
	item.Name = "GoogleChrome"
	item.DisplayName = statusActionNoError
	return item
}

// stubCancelSeams stubs the status check and the command runner, and counts
// the commands that would have run.
func stubCancelSeams(t *testing.T, onCommand func()) *int {
	t.Helper()
	ran := 0
	statusCheckStatus = fakeCheckStatus
	runCommand = func(string, []string) (string, error) {
		ran++
		if onCommand != nil {
			onCommand()
		}
		return "", nil
	}
	t.Cleanup(func() {
		statusCheckStatus = origCheckStatus
		runCommand = origRunCommand
	})
	return &ran
}

func TestInstallSkipsItemWithdrawnBeforeItStarts(t *testing.T) {
	ran := stubCancelSeams(t, nil)
	r := newTestRunner()
	r.Cancels = NewCancels()
	var events []string
	r.Emit = func(_ catalog.Item, state string, _ int, _ string) { events = append(events, state) }

	if !r.Cancels.Cancel("GoogleChrome") {
		t.Fatal("Cancel refused an item no run has touched")
	}
	if _, err := r.Install(cancelTestItem(), "install"); !errors.Is(err, ErrCanceled) {
		t.Fatalf("Install error = %v, want ErrCanceled", err)
	}
	if *ran != 0 || len(events) != 0 {
		t.Fatalf("a withdrawn item still ran: commands=%d events=%v", *ran, events)
	}
	if len(r.Report.FailedItems)+len(r.Report.InstalledItems) != 0 {
		t.Fatalf("a withdrawn item is neither failed nor installed: %#v", r.Report)
	}
}

func TestCancelDuringDownloadAbortsItAndSkipsTheCommand(t *testing.T) {
	ran := stubCancelSeams(t, nil)
	requested := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requested)
		<-r.Context().Done()
	}))
	defer ts.Close()

	r := newTestRunner()
	r.Cancels = NewCancels()
	r.CachePath = t.TempDir()
	r.URLPackages = ts.URL + "/"
	go func() {
		<-requested
		if !r.Cancels.Cancel("GoogleChrome") {
			t.Error("Cancel refused an item that was still downloading")
		}
	}()

	if _, err := r.Install(cancelTestItem(), "install"); !errors.Is(err, ErrCanceled) {
		t.Fatalf("Install error = %v, want ErrCanceled", err)
	}
	if *ran != 0 {
		t.Fatalf("the install command ran %d times after the cancel", *ran)
	}
	if len(r.Report.FailedItems) != 0 {
		t.Fatalf("an aborted download is not a failure: %#v", r.Report.FailedItems)
	}
}

func TestCancelIsRefusedOnceTheCommandStarts(t *testing.T) {
	r := newTestRunner()
	r.Cancels = NewCancels()
	accepted := true
	ran := stubCancelSeams(t, func() { accepted = r.Cancels.Cancel("GoogleChrome") })

	if _, err := r.Install(cancelTestItem(), "install"); err != nil {
		t.Fatalf("Install failed: %v", err)
	}
	if *ran != 1 || accepted {
		t.Fatalf("a running installer must not be cancelable: commands=%d accepted=%v", *ran, accepted)
	}
}

func TestCancelsForgetWithdrawalsAfterARunButNotActedItems(t *testing.T) {
	c := NewCancels()
	c.Cancel("Withdrawn")
	c.act("Acted")
	c.EndRun()

	// An admin-required item must not stay skipped after the run it was
	// withdrawn from.
	if c.withdrawn("Withdrawn") {
		t.Fatal("a withdrawal outlived its run")
	}
	// An item a run acted on stays refused until a new request resets it.
	if c.Cancel("Acted") {
		t.Fatal("Cancel accepted an item a run already acted on")
	}
	c.Reset("Acted")
	if !c.Cancel("Acted") {
		t.Fatal("Cancel refused an item after a new request reset it")
	}
}

func TestNilCancelsWithdrawNothing(t *testing.T) {
	var c *Cancels
	if c.Cancel("x") || c.withdrawn("x") || !c.act("x") {
		t.Fatal("a nil Cancels must leave every item to run")
	}
}
