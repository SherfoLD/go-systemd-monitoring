package main

import (
	"bytes"
	"fmt"
	"log"
	"net/http"

	"github.com/coreos/go-systemd/v22/dbus"
)

func main() {
	conn, err := dbus.New()
	if err != nil {
		log.Fatalf("Could not create a new connection object: %v", err)
	}
	defer conn.Close()

	err = conn.Subscribe()
	if err != nil {
		log.Fatalf("Could not subscribe to the bus: %v", err)
	}

	updateCh := make(chan *dbus.PropertiesUpdate, 256)
	errCh := make(chan error, 256)

	conn.SetPropertiesSubscriber(updateCh, errCh)

	for {
		select {
		case update := <-updateCh:
			if err := postToPushGateway(update); err != nil {
				log.Println("Failed to post to Pushgateway:", err)
			}
		case err := <-errCh:
			log.Println("Error from dbus:", err)
		}
	}
}

func postToPushGateway(update *dbus.PropertiesUpdate) error {
	activeState, ok := update.Changed["ActiveState"]
	if !ok {
		return fmt.Errorf("active state not found in dbus update")
	}

	data := fmt.Sprintf("# TYPE service_state gauge\nservice_state{service=\"%s\"} %d\n",
		update.UnitName, stateToValue(activeState.String()))

	url := "http://localhost:9091/metrics/job/top/instance/machine"

	req, err := http.NewRequest("POST", url, bytes.NewBufferString(data))
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("error sending request to Pushgateway: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("received non-ok status from Pushgateway: %s", resp.Status)
	}

	return nil
}

func stateToValue(state string) int {
	switch state {
	case "\"failed\"":
		return -1
	case "\"inactive\"":
		return 0
	case "\"active\"":
		return 1
	case "\"reloading\"":
		return 2
	case "\"activating\"":
		return 4
	case "\"deactivating\"":
		return 5
	default:
		panic("Unhandled dbus active state value")
	}
}
