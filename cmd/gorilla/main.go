package main

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/1dustindavis/gorilla/pkg/config"
	"github.com/1dustindavis/gorilla/pkg/gorillalog"
	"github.com/1dustindavis/gorilla/pkg/service"
)

var (
	managedRunFunc         = managedRun
	runServiceFunc         = runService
	sendServiceCommandFunc = service.SendCommand
	runServiceActionFunc   = service.RunAction
	serviceStatusFunc      = service.ServiceStatus
	protectAppDataFunc     = protectAppData
)

func main() {
	cfg := config.Get()
	if err := route(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func route(cfg config.Configuration) error {
	if cfg.ServiceInstall {
		// Before the install, so a reinstall over an existing service still
		// fixes the tree. The service applies it again at every start.
		if err := protectAppDataFunc(cfg.AppDataPath); err != nil {
			fmt.Fprintf(os.Stderr, "warning: unable to protect %s: %v\n", cfg.AppDataPath, err)
		}
		if err := runServiceActionFunc(cfg, "install"); err != nil {
			return err
		}
		fmt.Println("Service installed successfully")
		return nil
	}

	if cfg.ServiceRemove {
		if err := runServiceActionFunc(cfg, "remove"); err != nil {
			return err
		}
		fmt.Println("Service removed successfully")
		return nil
	}

	if cfg.ServiceStart {
		if err := runServiceActionFunc(cfg, "start"); err != nil {
			return err
		}
		fmt.Println("Service started successfully")
		return nil
	}

	if cfg.ServiceStop {
		if err := runServiceActionFunc(cfg, "stop"); err != nil {
			return err
		}
		fmt.Println("Service stopped successfully")
		return nil
	}

	if cfg.ServiceStatus {
		status, err := serviceStatusFunc(cfg)
		if err != nil {
			return err
		}
		fmt.Println("Service status:")
		fmt.Println(status)
		return nil
	}

	if cfg.ServiceCommand != "" {
		resp, err := sendServiceCommandFunc(cfg, cfg.ServiceCommand)
		if err != nil {
			return err
		}
		action := cfg.ServiceCommand
		if i := strings.Index(action, ":"); i >= 0 {
			action = action[:i]
		}
		action = strings.ToLower(strings.TrimSpace(action))

		if len(resp.Items) > 0 {
			for _, item := range resp.Items {
				fmt.Println(item)
			}
			if action == "streamoperationstatus" && resp.Message != "" {
				fmt.Println(resp.Message)
			}
			return nil
		}
		if resp.OperationID != "" {
			fmt.Printf("operationId: %s\n", resp.OperationID)
		}
		if resp.RequestedBy != "" {
			fmt.Printf("requestedBy: %s\n", resp.RequestedBy)
		}
		if resp.Message != "" {
			fmt.Println(resp.Message)
			return nil
		}
		switch action {
		case "listoptionalinstalls":
			fmt.Println("none")
		case "installitem":
			fmt.Println("InstallItem command completed successfully")
		case "removeitem":
			fmt.Println("RemoveItem command completed successfully")
		case "streamoperationstatus":
			fmt.Println("StreamOperationStatus command completed successfully")
		default:
			fmt.Println("Service command completed successfully")
		}
		return nil
	}

	if cfg.ServiceMode {
		return runServiceFunc(cfg)
	}

	_, err := managedRunFunc(cfg, nil, nil)
	return err
}

// runService protects the data directory, then hands over to the service
// manager. Protecting it at every start fixes a tree deployed by hand or by an
// older version. A failure is only logged: the run-time guard on admin-managed
// items still holds.
func runService(cfg config.Configuration) error {
	if err := gorillalog.NewLog(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	if err := protectAppDataFunc(cfg.AppDataPath); err != nil {
		slog.Warn("unable to protect the data directory", "path", cfg.AppDataPath, "err", err)
	}
	return service.Run(cfg, managedRunFunc)
}
