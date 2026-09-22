package proxmoxpbs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

var ErrCommandOutputLimit = errors.New("command output exceeded limit")

type Command struct {
	Path  string
	Args  []string
	Env   map[string]string
	Stdin []byte
}

type CommandResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

type CommandRunner interface {
	Run(ctx context.Context, command Command) (CommandResult, error)
}

type ExecRunner struct {
	MaxOutputBytes int64
}

func (runner ExecRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	limit := runner.MaxOutputBytes
	if limit <= 0 {
		limit = 4 << 20
	}
	stdout := &boundedBuffer{remaining: limit}
	stderr := &boundedBuffer{remaining: limit}
	process := exec.CommandContext(ctx, command.Path, command.Args...)
	process.Env = sanitizedEnvironment(command.Env)
	process.Stdin = bytes.NewReader(command.Stdin)
	process.Stdout = stdout
	process.Stderr = stderr

	err := process.Run()
	result := CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if process.ProcessState != nil {
		result.ExitCode = process.ProcessState.ExitCode()
	}
	if stdout.exceeded || stderr.exceeded {
		return result, ErrCommandOutputLimit
	}
	return result, err
}

func sanitizedEnvironment(overrides map[string]string) []string {
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		upperName := strings.ToUpper(name)
		if strings.HasPrefix(upperName, "PBS_") || strings.HasPrefix(upperName, "PROXMOX_OUTPUT_") {
			continue
		}
		environment = append(environment, item)
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		environment = append(environment, key+"="+overrides[key])
	}
	return environment
}

type boundedBuffer struct {
	buffer    bytes.Buffer
	remaining int64
	exceeded  bool
}

func (buffer *boundedBuffer) Write(data []byte) (int, error) {
	if int64(len(data)) > buffer.remaining {
		allowed := int(buffer.remaining)
		if allowed > 0 {
			_, _ = buffer.buffer.Write(data[:allowed])
		}
		buffer.remaining = 0
		buffer.exceeded = true
		return allowed, fmt.Errorf("%w", ErrCommandOutputLimit)
	}
	buffer.remaining -= int64(len(data))
	return buffer.buffer.Write(data)
}

func (buffer *boundedBuffer) Bytes() []byte {
	return append([]byte(nil), buffer.buffer.Bytes()...)
}
