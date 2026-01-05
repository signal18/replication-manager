//go:build clients
// +build clients

package clients

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/cluster"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var printDefaultsCmd = &cobra.Command{
	Use:   "print-defaults",
	Short: "Send mariadbd --print-defaults results to the monitor",
	Long:  `The print-defaults command sends the mariadbd --print-defaults command results to the monitor`,
	Run: func(cmd *cobra.Command, args []string) {
		if cliServerID == "" && cliServerHost == "" {
			fmt.Fprintf(os.Stderr, "API call error: %s", "No server id specified. Please use --id or --srv-host")
			os.Exit(1)
		}
		log.SetFormatter(&log.TextFormatter{})
		if needRefreshConfig() {
			cliInit(true)
			RunConfigPrintJobs()
		} else {
			fmt.Println("No need to refresh configuration.")
		}
	},
}

// RunConfigPrintJobs coordinates the fetching and printing of MariaDB defaults
func RunConfigPrintJobs() error {
	// Step 1: Fetch config receiver data
	receiverResp, err := fetchConfigReceiver()
	if err != nil {
		return fmt.Errorf("failed to fetch config receiver: %w", err)
	}

	if cliLogDir == "" {
		cliLogDir = os.TempDir()
	}

	// Step 2: Prepare log file paths
	dummyLog := filepath.Join(cliLogDir, "dummy.log")
	currentLog := filepath.Join(cliLogDir, "current.log")

	var wg sync.WaitGroup
	wg.Add(2)

	// Step 3: Start PrintDefaultsForFetchPush in a goroutine (using new push-based mechanism)
	go func() {
		defer wg.Done()
		err := PrintDefaultsForFetchPush(receiverResp, dummyLog)
		if err != nil {
			log.Printf("[dummy] Error: %v", err)
		} else {
			log.Printf("[dummy] Success. Output in %s", dummyLog)
		}
	}()

	// Step 4: Start PrintDefaultsFromPidFile in a goroutine
	go func() {
		defer wg.Done()
		err := PrintDefaultsFromPidFile(receiverResp, currentLog)
		if err != nil {
			log.Printf("[current] Error: %v", err)
		} else {
			log.Printf("[current] Success. Output in %s", currentLog)
		}
	}()

	// Step 5: Wait for both goroutines to finish
	wg.Wait()
	log.Println("Both configuration checks completed.")
	return nil
}

// PrintDefaultsFromPidFile checks the pid_file, reads the command line arguments, and executes the MariaDB command.
func PrintDefaultsFromPidFile(receiver *cluster.ConfigReceiverResponse, logFile string) error {
	// Step 1: If pid_file does not exist or is empty, fallback to mariadbd
	if receiver.CurrentPIDFile == "" || !fileExists(receiver.CurrentPIDFile) {
		return SendMariaDBDefaults(receiver.DefaultConfigPath, receiver.MonitorAddress+":"+receiver.CurrentConfigPort, logFile)
	}

	// Step 2: Read the PID from the pid_file
	pidData, err := os.ReadFile(receiver.CurrentPIDFile)
	if err != nil {
		return fmt.Errorf("failed to read pid file: %w", err)
	}

	pid := strings.TrimSpace(string(pidData))
	if pid == "" {
		return fmt.Errorf("pid file is empty")
	}

	// Step 3: Check if the process with this PID exists
	processPath := fmt.Sprintf("/proc/%s", pid)
	if _, err := os.Stat(processPath); os.IsNotExist(err) {
		return fmt.Errorf("process with PID %s not found", pid)
	}

	// Step 4: Read the command line arguments for this PID
	args, err := ReadCmdLineArgs(pid)
	if err != nil {
		return err
	}

	// Step 5: Find --defaults-file argument
	defaultsFile := ""
	for _, arg := range args {
		if strings.HasPrefix(arg, "--defaults-file=") {
			defaultsFile = strings.TrimPrefix(arg, "--defaults-file=")
			break
		}
	}

	// Step 6: If --defaults-file is not found, use the default config path
	if defaultsFile == "" {
		defaultsFile = receiver.DefaultConfigPath
	}

	// Step 7: Run the MariaDB command with the found or default --defaults-file
	return SendMariaDBDefaults(defaultsFile, receiver.MonitorAddress+":"+receiver.CurrentConfigPort, logFile)
}

// SendMariaDBDefaults runs the 'mariadbd' or 'mysqld' command, processes the output, and sends it over TCP to the receiverAddr.
// It also logs the output to the specified logFile.
func SendMariaDBDefaults(defaultsFile, receiverAddr, logFile string) error {
	// Step 1: Check if 'mariadbd' exists, otherwise fall back to 'mysqld'.
	command := "mariadbd"
	if !CommandExists(command) {
		fmt.Println("'mariadbd' not found, falling back to 'mysqld'.")
		command = "mysqld"
	}

	// Run the selected command with --print-defaults
	cmd := exec.Command(command, fmt.Sprintf("--defaults-file=%s", defaultsFile), "--print-defaults")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out // Capture stderr in the same buffer for logging and debugging

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("error executing command: %w\nstderr: %s", err, out.String())
	}

	// Step 2: Process the output and replace ' --' with newline
	processedOutput := strings.ReplaceAll(out.String(), " --", "\n--")

	// Step 3: Send the processed output over TCP
	conn, err := net.Dial("tcp", receiverAddr)
	if err != nil {
		return fmt.Errorf("error connecting to TCP address: %w", err)
	}
	defer conn.Close()

	// Write the processed data to the connection
	_, err = conn.Write([]byte(processedOutput))
	if err != nil {
		return fmt.Errorf("error sending data to TCP server: %w", err)
	}

	// Optionally log the output to the specified log file
	file, err := os.Create(logFile)
	if err != nil {
		return fmt.Errorf("error creating log file: %w", err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	_, err = writer.WriteString(processedOutput)
	if err != nil {
		return fmt.Errorf("error writing to log file: %w", err)
	}
	writer.Flush()

	fmt.Println("Output sent to", receiverAddr, "and logged to", logFile)
	return nil
}

// fileExists checks if the file exists.
func fileExists(filename string) bool {
	_, err := os.Stat(filename)
	return !os.IsNotExist(err)
}

// CommandExists checks if a command is available in the system's PATH using the `which` command.
func CommandExists(command string) bool {
	cmd := exec.Command("which", command)
	err := cmd.Run()

	// If `which` fails (exit status 1), the command doesn't exist in the PATH
	return err == nil
}

// ReadCmdLineArgs reads the command line arguments from the process' /proc/pid/cmdline file.
func ReadCmdLineArgs(pid string) ([]string, error) {
	cmdLinePath := fmt.Sprintf("/proc/%s/cmdline", pid)
	data, err := os.ReadFile(cmdLinePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read cmdline for PID %s: %w", pid, err)
	}

	// Split cmdline by null characters (\0), and return the arguments
	args := strings.Split(string(data), "\x00")
	return args, nil
}

// PrintDefaultsForFetch fetches a configuration from Replication Manager, extracts it, and runs mariadbd in dry-run mode.
func PrintDefaultsForFetch(receiver *cluster.ConfigReceiverResponse, logFile string) error {
	extractDir := "/bootstrap/dummy"

	// Step 1: Fetch and extract the configuration from Replication Manager
	err := fetchAndExtractConfig(receiver, extractDir)
	if err != nil {
		return fmt.Errorf("failed to fetch and extract configuration: %w", err)
	}

	// Step 2: Create the dummy.cnf file
	err = createDummyConfigFile(extractDir)
	if err != nil {
		return fmt.Errorf("failed to create dummy.cnf file: %w", err)
	}

	// Step 3: Run mariadbd with the dummy configuration file
	err = SendMariaDBDefaults(filepath.Join(extractDir, "dummy.cnf"), receiver.MonitorAddress+":"+receiver.DummyConfigPort, logFile)
	if err != nil {
		return fmt.Errorf("error during dry run (dummy): %w", err)
	}

	fmt.Println("Dry run (dummy) completed successfully.")
	return nil
}

// PrintDefaultsForFetchPush uses the new push-based mechanism where replication-manager sends the config via SST
func PrintDefaultsForFetchPush(receiver *cluster.ConfigReceiverResponse, logFile string) error {
	extractDir := "/bootstrap/dummy"

	// Ensure the extract directory exists
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory '%s': %w", extractDir, err)
	}

	// Step 1: Tell replication-manager to queue the config send and get SST port info
	var bearer = "Bearer " + cliToken
	var configURL string

	if cliServerID != "" {
		configURL = fmt.Sprintf("https://%s:%s/api/clusters/%s/servers/%s/config-dummy-sender",
			cliHost, cliPort, cliClusters[cliClusterIndex], cliServerID)
	} else {
		if cliServerPort == "" {
			cliServerPort = "3306"
		}
		configURL = fmt.Sprintf("https://%s:%s/api/clusters/%s/servers/%s/%s/config-dummy-sender",
			cliHost, cliPort, cliClusters[cliClusterIndex], cliServerHost, cliServerPort)
	}

	req, err := http.NewRequest("POST", configURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", bearer)

	resp, err := cliConn.Do(req)
	if err != nil {
		return fmt.Errorf("error requesting config send from '%s': %w", configURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to request config send: HTTP %d %s - %s", resp.StatusCode, resp.Status, string(bodyBytes))
	}

	// Parse the response to get SST port info
	var apiResponse struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		SSTPort string `json:"sst_port"`
		SSTHost string `json:"sst_host"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		return fmt.Errorf("failed to decode API response: %w", err)
	}

	fmt.Printf("Config send queued (status: %s)\n", apiResponse.Status)
	fmt.Printf("Config will be sent to %s:%s\n", apiResponse.SSTHost, apiResponse.SSTPort)

	// Step 2: Now open listener on the SST port we received from the API
	configFile := filepath.Join(extractDir, "config.tar.gz")

	listener, err := net.Listen("tcp", fmt.Sprintf(":%s", apiResponse.SSTPort))
	if err != nil {
		return fmt.Errorf("failed to listen on port %s: %w", apiResponse.SSTPort, err)
	}

	fmt.Printf("Listening for config on port %s (server's SST port)\n", apiResponse.SSTPort)

	// Channel to signal completion or error
	done := make(chan error, 1)

	// Step 3: Start goroutine to accept connection and receive file
	go func() {
		defer listener.Close()

		conn, err := listener.Accept()
		if err != nil {
			done <- fmt.Errorf("failed to accept connection: %w", err)
			return
		}
		defer conn.Close()

		// Set read timeout
		conn.SetReadDeadline(time.Now().Add(10 * time.Minute))

		file, err := os.Create(configFile)
		if err != nil {
			done <- fmt.Errorf("failed to create config file: %w", err)
			return
		}
		defer file.Close()

		// Copy data from connection to file
		written, err := io.Copy(file, conn)
		if err != nil {
			done <- fmt.Errorf("failed to receive config data: %w", err)
			return
		}

		fmt.Printf("Received %d bytes\n", written)
		done <- nil
	}()

	// Step 4: Wait for the file to be received (with timeout)
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("error receiving config: %w", err)
		}
	case <-time.After(10 * time.Minute):
		listener.Close()
		return fmt.Errorf("timeout waiting for config file")
	}

	fmt.Println("Config file received successfully")

	// Step 4: Extract the config tarball
	cmd := exec.Command("tar", "xzvf", configFile, "-C", extractDir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to extract config tarball: %w, stderr: %s", err, stderr.String())
	}

	// Set ownership for MySQL configuration directory to mysql:mysql (UID 999, GID 999)
	if err := os.Chown(filepath.Join(extractDir, "etc", "mysql"), 999, 999); err != nil {
		return fmt.Errorf("failed to set ownership for MySQL config: %w", err)
	}

	// Step 5: Create the dummy.cnf file
	err = createDummyConfigFile(extractDir)
	if err != nil {
		return fmt.Errorf("failed to create dummy.cnf file: %w", err)
	}

	// Step 6: Run mariadbd with the dummy configuration file
	err = SendMariaDBDefaults(filepath.Join(extractDir, "dummy.cnf"), receiver.MonitorAddress+":"+receiver.DummyConfigPort, logFile)
	if err != nil {
		return fmt.Errorf("error during dry run (dummy): %w", err)
	}

	fmt.Println("Dry run (dummy) completed successfully.")
	return nil
}

// fetchAndExtractConfig fetches the configuration using the new cookie-based push mechanism
// and extracts it to the specified directory.
// This function now uses the same mechanism as PrintDefaultsForFetchPush but is kept for backward compatibility.
func fetchAndExtractConfig(receiver *cluster.ConfigReceiverResponse, extractDir string) error {
	var bearer = "Bearer " + cliToken
	var configURL string

	// Ensure the extract directory exists
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory '%s': %w", extractDir, err)
	}

	// Step 1: Tell replication-manager to queue the config send and get SST port info
	if cliServerID != "" {
		configURL = fmt.Sprintf("https://%s:%s/api/clusters/%s/servers/%s/config-dummy-sender",
			cliHost, cliPort, cliClusters[cliClusterIndex], cliServerID)
	} else {
		if cliServerPort == "" {
			cliServerPort = "3306"
		}
		configURL = fmt.Sprintf("https://%s:%s/api/clusters/%s/servers/%s/%s/config-dummy-sender",
			cliHost, cliPort, cliClusters[cliClusterIndex], cliServerHost, cliServerPort)
	}

	req, err := http.NewRequest("POST", configURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", bearer)

	resp, err := cliConn.Do(req)
	if err != nil {
		return fmt.Errorf("error requesting config send from '%s': %w", configURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to request config send: HTTP %d %s - %s", resp.StatusCode, resp.Status, string(bodyBytes))
	}

	// Parse the response to get SST port info
	var apiResponse struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		SSTPort string `json:"sst_port"`
		SSTHost string `json:"sst_host"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		return fmt.Errorf("failed to decode API response: %w", err)
	}

	fmt.Printf("Config send queued (status: %s)\n", apiResponse.Status)
	fmt.Printf("Config will be sent to %s:%s\n", apiResponse.SSTHost, apiResponse.SSTPort)

	// Step 2: Now open listener on the SST port we received from the API
	configFile := filepath.Join(extractDir, "config.tar.gz")

	listener, err := net.Listen("tcp", fmt.Sprintf(":%s", apiResponse.SSTPort))
	if err != nil {
		return fmt.Errorf("failed to listen on port %s: %w", apiResponse.SSTPort, err)
	}

	fmt.Printf("Listening for config on port %s (server's SST port)\n", apiResponse.SSTPort)

	// Channel to signal completion or error
	done := make(chan error, 1)

	// Step 3: Start goroutine to accept connection and receive file
	go func() {
		defer listener.Close()

		conn, err := listener.Accept()
		if err != nil {
			done <- fmt.Errorf("failed to accept connection: %w", err)
			return
		}
		defer conn.Close()

		fmt.Println("Connection accepted, receiving config file...")

		// Create the output file
		outFile, err := os.Create(configFile)
		if err != nil {
			done <- fmt.Errorf("failed to create config file: %w", err)
			return
		}
		defer outFile.Close()

		// Copy the received data to file
		written, err := io.Copy(outFile, conn)
		if err != nil {
			done <- fmt.Errorf("failed to receive config file: %w", err)
			return
		}

		fmt.Printf("Received config file (%d bytes)\n", written)
		done <- nil
	}()

	// Step 4: Wait for reception with timeout
	select {
	case err := <-done:
		if err != nil {
			return err
		}
	case <-time.After(5 * time.Minute):
		listener.Close()
		return fmt.Errorf("timeout waiting for config file (waited 5 minutes)")
	}

	// Step 5: Extract the received tarball
	fmt.Println("Extracting config...")
	cmd := exec.Command("tar", "xzvf", configFile, "-C", extractDir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to extract config tarball: %w, stderr: %s", err, stderr.String())
	}

	// Step 6: Set ownership for MySQL configuration directory to mysql:mysql (UID 999, GID 999)
	if err := os.Chown(filepath.Join(extractDir, "etc", "mysql"), 999, 999); err != nil {
		// Log warning but don't fail if chown fails (might not have permissions)
		fmt.Printf("Warning: failed to set ownership for MySQL config: %v\n", err)
	}

	fmt.Println("Config extracted successfully")
	return nil
}

// createDummyConfigFile creates the dummy.cnf file for the dry-run.
func createDummyConfigFile(baseDir string) error {
	dummyConfigFile := filepath.Join(baseDir, "dummy.cnf")
	file, err := os.Create(dummyConfigFile)
	if err != nil {
		return fmt.Errorf("failed to create dummy.cnf: %w", err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	_, err = writer.WriteString("[mysqld]\n")
	if err != nil {
		return fmt.Errorf("failed to write to dummy.cnf: %w", err)
	}

	_, err = writer.WriteString("!includedir /bootstrap/dummy/etc/mysql/conf.d\n")
	if err != nil {
		return fmt.Errorf("failed to write to dummy.cnf: %w", err)
	}

	_, err = writer.WriteString("!includedir /bootstrap/dummy/etc/mysql/custom.d\n")
	if err != nil {
		return fmt.Errorf("failed to write to dummy.cnf: %w", err)
	}

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush dummy.cnf file: %w", err)
	}

	// Set ownership for dummy.cnf file to mysql:mysql (UID 999, GID 999)
	if err := os.Chown(dummyConfigFile, 999, 999); err != nil {
		return fmt.Errorf("failed to set ownership for dummy.cnf: %w", err)
	}

	return nil
}

func fetchConfigReceiver() (*cluster.ConfigReceiverResponse, error) {
	//var r string
	var bearer = "Bearer " + cliToken
	var urlpost string

	if cliServerID != "" {
		urlpost = fmt.Sprintf("https://%s:%s/api/clusters/%s/servers/%s/config-receiver", cliHost, cliPort, cliClusters[cliClusterIndex], cliServerID)
	} else {
		if cliServerPort == "" {
			cliServerPort = "3306"
		}
		urlpost = fmt.Sprintf("https://%s:%s/api/clusters/%s/servers/%s/%s/config-receiver", cliHost, cliPort, cliClusters[cliClusterIndex], cliServerHost, cliServerPort)
	}

	req, err := http.NewRequest("GET", urlpost, nil)
	if err != nil {
		return nil, err
	}
	//	ctx, _ := context.WithTimeout(context.Background(), 600*time.Second)
	req.Header.Set("Authorization", bearer)
	resp, err := cliConn.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch config-receiver: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code %d from %s", resp.StatusCode, urlpost)
	}

	var receiverResp cluster.ConfigReceiverResponse
	if err := json.NewDecoder(resp.Body).Decode(&receiverResp); err != nil {
		return nil, fmt.Errorf("failed to decode JSON response: %w", err)
	}

	return &receiverResp, nil
}

func needRefreshConfig() bool {
	//var r string
	var urlpost string = "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cfgGroup + "/servers/"
	if cliServerID != "" {
		urlpost += cliServerID + "/need-config-refresh"
	} else {
		if cliServerPort == "" {
			cliServerPort = "3306"
		}
		urlpost += cliServerHost + "/" + cliServerPort + "/need-config-refresh"
	}

	_, err := cliAPICmd(urlpost, nil)
	if err != nil {
		return false
	}

	return true
}
