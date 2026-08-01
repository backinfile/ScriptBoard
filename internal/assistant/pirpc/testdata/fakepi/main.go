package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

type command struct {
	ID, Type, Message, Provider, ModelID string
}

func main() {
	arguments := os.Args[1:]
	if !containsPair(arguments, "--mode", "rpc") || !slices.Contains(arguments, "--no-tools") || !slices.Contains(arguments, "--no-context-files") || !slices.Contains(arguments, "--no-approve") {
		fmt.Fprintln(os.Stderr, "unsafe fake Pi launch arguments")
		os.Exit(2)
	}
	piHome := os.Getenv("PI_CODING_AGENT_DIR")
	if piHome == "" || os.Getenv("SCRIPTBOARD_PI_API_KEY") == "" || os.Getenv("PI_OFFLINE") != "1" || os.Getenv("PI_SKIP_VERSION_CHECK") != "1" || os.Getenv("PI_TELEMETRY") != "0" {
		fmt.Fprintln(os.Stderr, "missing private Pi environment")
		os.Exit(3)
	}
	if _, err := os.Stat(filepath.Join(piHome, "models.json")); err != nil {
		fmt.Fprintln(os.Stderr, "missing models.json")
		os.Exit(4)
	}
	sessionDir := argumentValue(arguments, "--session-dir")
	if sessionDir == "" {
		fmt.Fprintln(os.Stderr, "missing session directory")
		os.Exit(5)
	}
	resumed := slices.Contains(arguments, "--continue")
	if err := os.WriteFile(filepath.Join(sessionDir, "fixture-session.jsonl"), []byte("{}\n"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "cannot persist fixture session")
		os.Exit(6)
	}

	reader := bufio.NewScanner(os.Stdin)
	for reader.Scan() {
		var request command
		if err := json.Unmarshal(reader.Bytes(), &request); err != nil {
			os.Exit(5)
		}
		switch request.Type {
		case "set_model":
			if request.Provider != "scriptboard-provider" || request.ModelID == "" {
				fmt.Printf("{\"id\":%q,\"type\":\"response\",\"command\":\"set_model\",\"success\":false}\n", request.ID)
				continue
			}
			fmt.Printf("{\"id\":%q,\"type\":\"response\",\"command\":\"set_model\",\"success\":true}\n", request.ID)
		case "prompt":
			fmt.Printf("{\"id\":%q,\"type\":\"response\",\"command\":\"prompt\",\"success\":true}\n", request.ID)
			if request.Message == "hang" {
				continue
			}
			if request.Message == "provider-error" {
				fmt.Println(`{"type":"message_update","message":{"role":"assistant","stopReason":"error"},"assistantMessageEvent":{"type":"error","reason":"error"}}`)
				fmt.Println(`{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"error"}}`)
				fmt.Println(`{"type":"agent_end","messages":[{"role":"assistant","content":[],"stopReason":"error"}],"willRetry":false}`)
				fmt.Println(`{"type":"agent_settled"}`)
				continue
			}
			delta := "fixture response"
			if resumed {
				delta = "resumed fixture response"
			}
			fmt.Printf("{\"type\":\"message_update\",\"assistantMessageEvent\":{\"type\":\"text_delta\",\"delta\":%q}}\n", delta)
			fmt.Println(`{"type":"agent_settled"}`)
		case "abort":
			fmt.Printf("{\"id\":%q,\"type\":\"response\",\"command\":\"abort\",\"success\":true}\n", request.ID)
			return
		case "get_state":
			fmt.Printf("{\"id\":%q,\"type\":\"response\",\"command\":\"get_state\",\"success\":true,\"data\":{\"isStreaming\":false}}\n", request.ID)
		default:
			fmt.Printf("{\"id\":%q,\"type\":\"response\",\"command\":%q,\"success\":false}\n", request.ID, request.Type)
		}
	}
}

func argumentValue(arguments []string, name string) string {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == name {
			return arguments[index+1]
		}
	}
	return ""
}

func containsPair(arguments []string, name, value string) bool {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == name && arguments[index+1] == value {
			return true
		}
	}
	return false
}
