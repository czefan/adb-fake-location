// A filesystem-backed ADB simulator for subprocess/browser smoke tests only.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

type state struct {
	Mode         string `json:"mode"`
	Mock         bool   `json:"mock"`
	Offline      bool   `json:"offline"`
	FailRemove   bool   `json:"fail_remove"`
	LastLocation string `json:"last_location"`
	Injections   int    `json:"injections"`
}

func main() {
	if mode := os.Getenv("FAKE_ADB_HELP_MODE"); mode != "" {
		fmt.Println("Location service commands:")
		if mode != "incomplete" {
			fmt.Println("providers add-test-provider set-test-provider-enabled set-test-provider-location remove-test-provider")
		}
		if mode == "denied" {
			fmt.Fprintln(os.Stderr, "Permission Denial: simulated")
		}
		if mode == "other-exit" {
			os.Exit(1)
		}
		os.Exit(255)
	}
	if len(os.Args) > 1 && os.Args[1] == "test-timeout" {
		time.Sleep(10 * time.Second)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "test-error" {
		fmt.Println("Error: simulated shell error")
		return
	}
	path := os.Getenv("FAKE_ADB_STATE")
	if path == "" {
		fmt.Fprintln(os.Stderr, "FAKE_ADB_STATE is required; no real device is accessed")
		os.Exit(1)
	}
	s := state{Mode: "ignore"}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &s); err != nil {
			panic(err)
		}
	}
	args := os.Args[1:]
	if len(args) >= 2 && args[0] == "-s" {
		if args[1] != "test-device" {
			fmt.Fprintln(os.Stderr, "unknown device")
			os.Exit(1)
		}
		args = args[2:]
	}
	cmd := strings.Join(args, " ")
	if cmd == "devices -l" {
		deviceState := "device"
		if s.Offline {
			deviceState = "offline"
		}
		fmt.Printf("List of devices attached\ntest-device %s model:TestPhone\n", deviceState)
		return
	}
	if s.Offline {
		fmt.Fprintln(os.Stderr, "device offline")
		os.Exit(1)
	}
	switch cmd {
	case "version":
		fmt.Println("Android Debug Bridge version TEST-ONLY")
		return
	case "shell getprop ro.build.version.sdk":
		fmt.Println("31")
		return
	case "shell cmd location help":
		fmt.Println("providers add-test-provider set-test-provider-enabled set-test-provider-location remove-test-provider")
		return
	case "shell cmd location is-location-enabled":
		fmt.Println("true")
		return
	case "shell id -u":
		fmt.Println("2000")
		return
	case "shell cmd appops get 2000 android:mock_location":
		fmt.Println("Uid mode: MOCK_LOCATION: " + s.Mode)
		return
	case "shell dumpsys location":
		if s.Mock {
			fmt.Println("gps provider [mock]:")
		} else {
			fmt.Println("gps provider:")
		}
		return
	}
	switch {
	case strings.HasPrefix(cmd, "shell cmd appops set 2000 android:mock_location "):
		s.Mode = args[len(args)-1]
	case strings.Contains(cmd, " add-test-provider "):
		s.Mock = true
	case strings.Contains(cmd, " set-test-provider-enabled "):
	case strings.Contains(cmd, " set-test-provider-location "):
		if !s.Mock {
			fmt.Fprintln(os.Stderr, "test provider absent")
			os.Exit(1)
		}
		for i, arg := range args {
			if arg == "--location" {
				s.LastLocation = args[i+1]
			}
		}
		s.Injections++
	case strings.Contains(cmd, " remove-test-provider "):
		if s.FailRemove {
			fmt.Fprintln(os.Stderr, "simulated cleanup failure")
			os.Exit(1)
		}
		s.Mock = false
	default:
		fmt.Fprintln(os.Stderr, "unknown test command: "+cmd)
		os.Exit(1)
	}
	data, _ := json.Marshal(s)
	if err := os.WriteFile(path, data, 0600); err != nil {
		panic(err)
	}
}
