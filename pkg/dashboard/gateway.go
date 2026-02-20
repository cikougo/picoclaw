package dashboard

import (
	"bufio"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"syscall"
	"time"
)

const maxLogLines = 500

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

type GatewayState string

const (
	StateStopped  GatewayState = "stopped"
	StateStarting GatewayState = "starting"
	StateRunning  GatewayState = "running"
	StateStopping GatewayState = "stopping"
	StateError    GatewayState = "error"
)

type GatewayStatus struct {
	State        GatewayState `json:"state"`
	PID          int          `json:"pid,omitempty"`
	Uptime       *int         `json:"uptime,omitempty"`
	RestartCount int          `json:"restart_count"`
}

type GatewayManager struct {
	mu           sync.Mutex
	cmd          *exec.Cmd
	state        GatewayState
	startTime    time.Time
	restartCount int
	logs         []string
	logStart     int // ring buffer start index
	logCount     int
	done         chan struct{}
}

func NewGatewayManager() *GatewayManager {
	return &GatewayManager{
		state: StateStopped,
		logs:  make([]string, maxLogLines),
	}
}

func (g *GatewayManager) Start() error {
	g.mu.Lock()
	if g.cmd != nil && g.cmd.Process != nil && g.cmd.ProcessState == nil {
		g.mu.Unlock()
		return nil // already running
	}

	g.state = StateStarting

	// Find the picoclaw binary path (same binary as the running process)
	binary, err := os.Executable()
	if err != nil {
		binary = "picoclaw"
	}

	g.cmd = exec.Command(binary, "gateway")
	g.cmd.Env = os.Environ()

	stdout, err := g.cmd.StdoutPipe()
	if err != nil {
		g.state = StateError
		g.appendLog("Failed to create stdout pipe: " + err.Error())
		g.mu.Unlock()
		return err
	}
	g.cmd.Stderr = g.cmd.Stdout // merge stderr into stdout

	if err := g.cmd.Start(); err != nil {
		g.state = StateError
		g.appendLog("Failed to start gateway: " + err.Error())
		g.mu.Unlock()
		return err
	}

	g.state = StateRunning
	g.startTime = time.Now()
	g.done = make(chan struct{})
	g.mu.Unlock()

	// Read output in background
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 64*1024)
		for scanner.Scan() {
			line := ansiEscape.ReplaceAllString(scanner.Text(), "")
			g.mu.Lock()
			g.appendLog(line)
			g.mu.Unlock()
		}
	}()

	// Wait for process exit in background
	go func() {
		err := g.cmd.Wait()
		g.mu.Lock()
		if g.state == StateRunning {
			g.state = StateError
			if err != nil {
				g.appendLog("Gateway exited: " + err.Error())
			} else {
				g.appendLog("Gateway exited with code 0")
			}
		}
		g.mu.Unlock()
		close(g.done)
	}()

	return nil
}

func (g *GatewayManager) Stop() {
	g.mu.Lock()
	if g.cmd == nil || g.cmd.Process == nil || g.cmd.ProcessState != nil {
		g.state = StateStopped
		g.mu.Unlock()
		return
	}

	g.state = StateStopping
	proc := g.cmd.Process
	done := g.done
	g.mu.Unlock()

	// Send SIGTERM
	_ = proc.Signal(syscall.SIGTERM)

	// Wait up to 10 seconds for graceful shutdown
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()

	select {
	case <-done:
	case <-timer.C:
		_ = proc.Kill()
		<-done
	}

	g.mu.Lock()
	g.state = StateStopped
	g.mu.Unlock()
}

func (g *GatewayManager) Restart() {
	g.Stop()
	g.mu.Lock()
	g.restartCount++
	g.mu.Unlock()
	_ = g.Start()
}

func (g *GatewayManager) GetStatus() GatewayStatus {
	g.mu.Lock()
	defer g.mu.Unlock()

	status := GatewayStatus{
		State:        g.state,
		RestartCount: g.restartCount,
	}

	if g.cmd != nil && g.cmd.Process != nil && g.cmd.ProcessState == nil {
		status.PID = g.cmd.Process.Pid
	}

	if g.state == StateRunning && !g.startTime.IsZero() {
		uptime := int(time.Since(g.startTime).Seconds())
		status.Uptime = &uptime
	}

	return status
}

func (g *GatewayManager) GetLogs() []string {
	g.mu.Lock()
	defer g.mu.Unlock()

	result := make([]string, 0, g.logCount)
	for i := 0; i < g.logCount; i++ {
		idx := (g.logStart + i) % maxLogLines
		result = append(result, g.logs[idx])
	}
	return result
}

// appendLog adds a line to the ring buffer. Caller must hold g.mu.
func (g *GatewayManager) appendLog(line string) {
	if g.logCount < maxLogLines {
		g.logs[g.logCount] = line
		g.logCount++
	} else {
		g.logs[g.logStart] = line
		g.logStart = (g.logStart + 1) % maxLogLines
	}
}
