package main

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/coreos/go-systemd/v22/dbus"
)

var (
	serviceStateMap = make(map[string]string)
	mapMutex        sync.Mutex
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

	// Start a goroutine to periodically post to Pushgateway
	go func() {
		for {
			time.Sleep(15 * time.Second)
			if err := postAllToPushGateway(); err != nil {
				log.Println("Failed to post to Pushgateway:", err)
			}
		}
	}()

	for {
		select {
		case update := <-updateCh:
			processUpdate(update)
		case err := <-errCh:
			log.Println("Error from dbus:", err)
		}
	}
}

func processUpdate(update *dbus.PropertiesUpdate) {
	activeState, ok := update.Changed["ActiveState"]
	if !ok {
		log.Printf("Active state not found in dbus update for unit: %s", update.UnitName)
		return
	}

	mapMutex.Lock()
	serviceStateMap[update.UnitName] = activeState.String()
	mapMutex.Unlock()
}

func postAllToPushGateway() error {
	mapMutex.Lock()
	defer mapMutex.Unlock()

	var buffer bytes.Buffer
	buffer.WriteString("# TYPE service_state gauge\n")

	for service, state := range serviceStateMap {
		buffer.WriteString(fmt.Sprintf("service_state{service=\"%s\"} %d\n", service, stateToValue(state)))
	}

	url := "http://localhost:9091/metrics/job/top/instance/machine"
	req, err := http.NewRequest("POST", url, &buffer)
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
