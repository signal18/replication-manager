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
		cliInit(true)
		RunConfigPrintJobs()
	},
}

// RunConfigPrintJobs coordinates the fetching and printing of MariaDB defaults
func RunConfigPrintJobs() error {
	// Step 1: Fetch config receiver data
	receiverResp, err := fetchConfigReceiver()
	if err != nil {
		return fmt.Errorf("failed to fetch config receiver: %w", err)
	}

	// Step 2: Prepare log file paths
	dummyLog := filepath.Join(os.TempDir(), "dummy.log")
	currentLog := filepath.Join(os.TempDir(), "current.log")

	var wg sync.WaitGroup
	wg.Add(2)

	// Step 3: Start PrintDefaultsForFetch in a goroutine
	go func() {
		defer wg.Done()
		err := PrintDefaultsForFetch(receiverResp, dummyLog)
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

// fetchAndExtractConfig fetches the configuration from the provided URL and extracts it to the specified directory.
func fetchAndExtractConfig(receiver *cluster.ConfigReceiverResponse, extractDir string) error {
	var configURL string
	// Construct the config URL
	if cliServerID != "" {
		configURL = fmt.Sprintf("https://%s:%s/api/clusters/%s/servers/%s/config", cliHost, cliPort, cliClusters[cliClusterIndex], cliServerID)
	} else {
		if cliServerPort == "" {
			cliServerPort = "3306"
		}
		configURL = fmt.Sprintf("https://%s:%s/api/clusters/%s/servers/%s/%s/config", cliHost, cliPort, cliClusters[cliClusterIndex], cliServerHost, cliServerPort)
	}

	// Send a GET request to the specified URL
	resp, err := http.Get(configURL)
	if err != nil {
		return fmt.Errorf("error fetching config from '%s': %w", configURL, err)
	}
	defer resp.Body.Close()

	// Check for a successful response status code (200 OK)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch config: HTTP %d %s", resp.StatusCode, resp.Status)
	}

	// Read the response body into a byte slice
	configData, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response body: %w", err)
	}

	// Remove the existing directory and create a new one
	if err := os.RemoveAll(extractDir); err != nil {
		return fmt.Errorf("failed to remove existing directory '%s': %w", extractDir, err)
	}

	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory '%s': %w", extractDir, err)
	}

	// Write the config tarball to a temporary file
	configFile := filepath.Join(extractDir, "config.tar.gz")
	if err := os.WriteFile(configFile, configData, 0644); err != nil {
		return fmt.Errorf("failed to write config file '%s': %w", configFile, err)
	}

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
