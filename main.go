package main

import (
        "context"
        "encoding/json"
        "fmt"
        "io/ioutil"
        "net"
        "net/http"
        "strings"
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
        Interval   int    `yaml:"interval"` // Interval in seconds
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
        fmt.Printf("ServerName: %s, Username: %s, LogRoom: %s, Interval: %d seconds\n",
                config.ServerName, config.Username, config.LogRoom, config.Interval)

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
        loginResp, err := client.Login(ctx, &mautrix.ReqLogin{
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

        // Set the access token explicitly
        client.AccessToken = loginResp.AccessToken
        fmt.Printf("Logged in successfully as %s\n", config.Username)

        // Run the server check loop
        runServerCheckLoop(ctx, client)
}

// resolveMatrixServer resolves the actual Matrix server URL using .well-known, DNS SRV, or fallback to server-name.com:8448
func resolveMatrixServer(server string) (string, error) {
        // 1. Try .well-known delegation
        url := fmt.Sprintf("https://%s/.well-known/matrix/server", server)
        resp, err := http.Get(url)
        if err == nil {
                defer resp.Body.Close()

                if resp.StatusCode == http.StatusOK {
                        var result struct {
                                Server string `json:"m.server"`
                        }
                        err = json.NewDecoder(resp.Body).Decode(&result)
                        if err == nil && result.Server != "" {
                                return result.Server, nil
                        }
                }
        }

        // 2. Try DNS SRV record for _matrix._tcp.server-name.com
        _, srvRecords, err := net.LookupSRV("matrix", "tcp", server)
        if err == nil && len(srvRecords) > 0 {
                srv := srvRecords[0] // Use the first SRV record
                return fmt.Sprintf("%s:%d", strings.Trim(srv.Target, "."), srv.Port), nil
        }

        // 3. Fallback to server-name.com:8448
        return fmt.Sprintf("%s:8448", server), nil
}


// runServerCheckLoop performs checks for offline servers at the specified interval
func runServerCheckLoop(ctx context.Context, client *mautrix.Client) {
        for {
                fmt.Println("Checking server statuses...")

                // Get all joined rooms
                joinedRooms, err := client.JoinedRooms(ctx)
                if err != nil {
                        fmt.Println("Failed to fetch joined rooms:", err)
                        time.Sleep(time.Duration(config.Interval) * time.Second)
                        continue
                }

                // Process each room
                for _, roomID := range joinedRooms.JoinedRooms {
                        // Skip the log room
                        if id.RoomID(roomID) == id.RoomID(config.LogRoom) { // Convert config.LogRoom to id.RoomID
                                fmt.Printf("Skipping log room: %s\n", config.LogRoom)
                                continue
                        }

                        // Fetch room details (alias and title)
                        roomAlias, roomTitle := getRoomDetails(ctx, client, id.RoomID(roomID))

                        // Format the room description
                        roomDescription := fmt.Sprintf("%s - %s ( %s )", roomAlias, roomTitle, roomID)
                        fmt.Println("Testing servers in room:", roomDescription)

                        // Fetch members of the room
                        resp, err := client.JoinedMembers(ctx, id.RoomID(roomID))
                        if err != nil {
                                fmt.Printf("Failed to get joined members for room %s: %v\n", roomID, err)
                                continue
                        }

                        // Check server statuses for the room
                        var serverStatus []string
                        var failedServers []string

                        for userID := range resp.Joined {
                                server := extractDomain(string(userID)) // Convert id.UserID to string
                                status := checkServer(ctx, client, server)

                                // Add to full status list
                                serverStatus = append(serverStatus, fmt.Sprintf("%s - %s", server, status))

                                // Add only failed servers to the failed list
                                if strings.HasPrefix(status, "Failed") {
                                        failedServers = append(failedServers, fmt.Sprintf("%s - %s", server, status))
                                }
                        }

                        // Combine the full status message for the console
                        fullStatusMessage := fmt.Sprintf("Server statuses in room %s:\n%s", roomDescription, strings.Join(serverStatus, "\n"))
                        fmt.Println(fullStatusMessage)

                        // Send only failed servers to the Matrix logroom
                        if len(failedServers) > 0 {
                                failedStatusMessage := fmt.Sprintf("Failed servers in room %s:\n%s", roomDescription, strings.Join(failedServers, "\n"))
                                sendMessageToRoom(ctx, client, id.RoomID(config.LogRoom), failedStatusMessage)
                        } else {
                                // If all servers are OK, send a success message to the logroom
                                successMessage := fmt.Sprintf("All Servers in room %s are OK", roomDescription)
                                sendMessageToRoom(ctx, client, id.RoomID(config.LogRoom), successMessage)
                        }
                }

                // Print waiting message to console
                fmt.Printf("Waiting for %d seconds\n", config.Interval)

                // Wait for the specified interval before checking again
                time.Sleep(time.Duration(config.Interval) * time.Second)
        }
}



const CanonicalAliasEventType = "m.room.canonical_alias" // Define the event type as a string

// getRoomDetails fetches the main alias and title of a room
func getRoomDetails(ctx context.Context, client *mautrix.Client, roomID id.RoomID) (string, string) {
        // Fetch the room name (title)
        var roomName struct {
                Name string `json:"name"`
        }
        err := client.StateEvent(ctx, roomID, event.StateRoomName, "", &roomName)
        if err != nil || roomName.Name == "" {
                roomName.Name = "(unknown title)"
        }

        // Fetch the canonical alias
        canonicalAliasType := event.NewEventType("m.room.canonical_alias") // Create the type for m.room.canonical_alias
        var canonicalAlias struct {
                Alias string `json:"alias"`
        }
        err = client.StateEvent(ctx, roomID, canonicalAliasType, "", &canonicalAlias)
        if err != nil || canonicalAlias.Alias == "" {
                fmt.Printf("No canonical alias found for room %s\n", roomID)
                return roomID.String(), roomName.Name // Use Room ID as fallback for alias
        }

        // Use the canonical alias as the main alias
        return canonicalAlias.Alias, roomName.Name
}




// checkServer resolves and checks the online status of a server
func checkServer(ctx context.Context, client *mautrix.Client, server string) string {
        matrixServer, err := resolveMatrixServer(server)
        if err != nil {
                return fmt.Sprintf("Failed (Delegation Failed: %v)", err)
        }

        if checkServerOnline(matrixServer) {
                return "OK"
        }
        return "Failed (Unreachable)"
}

// extractDomain extracts the domain part of a Matrix UserID
func extractDomain(userID string) string {
        parts := strings.Split(userID, ":")
        if len(parts) > 1 {
                return parts[1] // Return the domain part after ":"
        }
        return ""
}

// checkServerOnline checks if a server is online by sending a GET request to the Matrix federation version endpoint
func checkServerOnline(server string) bool {
        url := fmt.Sprintf("https://%s/_matrix/federation/v1/version", server)
        client := &http.Client{
                Timeout: 5 * time.Second,
        }
        resp, err := client.Get(url)
        if err != nil {
                fmt.Printf("Failed to reach server %s: %v\n", server, err)
                return false
        }
        defer resp.Body.Close()

        // Check if the response is valid JSON
        var result map[string]interface{}
        err = json.NewDecoder(resp.Body).Decode(&result)
        if err != nil {
                fmt.Printf("Invalid JSON response from server %s: %v\n", server, err)
                return false
        }
        return true
}

// sendMessageToRoom sends a message to a Matrix room
func sendMessageToRoom(ctx context.Context, client *mautrix.Client, roomID id.RoomID, message string) error {
        _, err := client.SendText(ctx, roomID, message)
        return err
}

func loadConfig(path string) error {
        fmt.Printf("Loading configuration from: %s\n", path)
        data, err := ioutil.ReadFile(path)
        if err != nil {
                return err
        }
        return yaml.Unmarshal(data, &config)
}
