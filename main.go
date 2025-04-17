package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"time"

	"gopkg.in/yaml.v3"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// Config represents the structure of the YAML configuration file
type Config struct {
	ServerName string `yaml:"servername"`
	Username   string `yaml:"username"`
	Password   string `yaml:"password"`
	LogRoom    string `yaml:"logroom"`
}

var config Config

func main() {
	fmt.Println("Starting Matrix client...")

	// Load the configuration
	err := loadConfig("config.yaml")
	if err != nil {
		fmt.Println("Failed to load configuration:", err)
		return
	}

	fmt.Println("Configuration loaded successfully.")
	fmt.Printf("ServerName: %s, Username: %s, LogRoom: %s\n", config.ServerName, config.Username, config.LogRoom)

	// Validate username format
	fmt.Println("Validating username format...")
	if _, _, err := id.UserID(config.Username).ParseAndValidate(); err != nil {
		fmt.Println("Invalid username in configuration:", err)
		return
	}
	fmt.Println("Username is valid.")

	// Create a new Matrix client
	fmt.Println("Creating Matrix client...")
	client, err := mautrix.NewClient(config.ServerName, "", "")
	if err != nil {
		fmt.Println("Failed to create Matrix client:", err)
		return
	}
	fmt.Println("Matrix client created.")

	// Log in to the Matrix account
	fmt.Println("Logging in...")
	ctx := context.Background()
	_, err = client.Login(ctx, &mautrix.ReqLogin{
		Type: mautrix.AuthTypePassword,
		Identifier: mautrix.UserIdentifier{
			Type: mautrix.IdentifierTypeUser,
			User: config.Username,
		},
		Password: config.Password,
	})
	if err != nil {
		fmt.Println("Failed to log in:", err)
		return
	}
	fmt.Printf("Logged in successfully as %s\n", config.Username)

	// Add event handler for room membership events
	fmt.Println("Adding event handler for room membership events...")
	syncer := client.Syncer.(*mautrix.DefaultSyncer)
	syncer.OnEventType(event.StateMember, func(_ context.Context, ev *event.Event) {
		fmt.Printf("Processing event: %+v\n", ev)

		// Validate sender's UserID
		fmt.Printf("Validating sender UserID: %s\n", ev.Sender)
		if _, _, err := ev.Sender.ParseAndValidate(); err != nil {
			fmt.Printf("Invalid sender UserID: %s, error: %v\n", ev.Sender, err)
			return
		}

		// Validate state key (if present)
		if ev.StateKey != nil {
			stateKey := id.UserID(*ev.StateKey)
			fmt.Printf("Validating state key UserID: %s\n", stateKey)
			if _, _, err := stateKey.ParseAndValidate(); err != nil {
				fmt.Printf("Invalid state key UserID: %s, error: %v\n", *ev.StateKey, err)
				return
			}
		}

		// Handle membership join events
		if ev.Content.AsMember().Membership == event.MembershipJoin {
			fmt.Printf("Membership join event detected for room: %s\n", ev.RoomID)
			handleRoom(ctx, client, ev.RoomID)
		}
	})

	// Start syncing with a custom error-handling loop
	fmt.Println("Starting sync...")
	for {
		err = client.Sync()
		if err != nil {
			fmt.Printf("Sync failed with error: %v\n", err)
			fmt.Println("Retrying sync in 5 seconds...")
			time.Sleep(5 * time.Second)
		} else {
			fmt.Println("Sync completed successfully.")
			break
		}
	}
}

func handleRoom(ctx context.Context, client *mautrix.Client, roomID id.RoomID) {
	fmt.Printf("Handling room: %s\n", roomID)

	// Get joined members
	fmt.Printf("Fetching joined members for room: %s\n", roomID)
	resp, err := client.JoinedMembers(ctx, roomID)
	if err != nil {
		fmt.Printf("Failed to get joined members for room %s: %v\n", roomID, err)
		return
	}

	fmt.Printf("Joined members fetched successfully for room: %s\n", roomID)
	for userID := range resp.Joined {
		// Validate each UserID
		fmt.Printf("Validating UserID: %s\n", userID)
		if _, _, err := userID.ParseAndValidate(); err != nil {
			fmt.Printf("Invalid UserID: %s, error: %v\n", userID, err)
			continue
		}

		fmt.Printf("Valid UserID: %s\n", userID)
	}
}

func loadConfig(path string) error {
	fmt.Printf("Loading configuration from: %s\n", path)
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, &config)
}
