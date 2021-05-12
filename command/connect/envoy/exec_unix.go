// +build linux darwin

package envoy

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// testSelfExecOverride is a way for the tests to no fork-bomb themselves by
// self-executing the whole test suite for each case recursively. It's gross but
// the least gross option I could think of.
var testSelfExecOverride string

func isHotRestartOption(s string) bool {
	restartOpts := []string{
		"--restart-epoch",
		"--hot-restart-version",
		"--drain-time-s",
		"--parent-shutdown-time-s",
	}
	for _, opt := range restartOpts {
		if s == opt {
			return true
		}
		if strings.HasPrefix(s, opt+"=") {
			return true
		}
	}
	return false
}

func hasHotRestartOption(argSets ...[]string) bool {
	for _, args := range argSets {
		for _, opt := range args {
			if isHotRestartOption(opt) {
				return true
			}
		}
	}
	return false
}

func hasMaxObjNameLenOption(argSets ...[]string) bool {
	for _, args := range argSets {
		for _, opt := range args {
			if opt == "--max-obj-name-len" {
				return true
			}
		}
	}
	return false
}

func makeBootstrapPipe(bootstrapJSON []byte) (string, error) {
	pipeFile := filepath.Join(os.TempDir(),
		fmt.Sprintf("envoy-%x-bootstrap.json", time.Now().UnixNano()+int64(os.Getpid())))

	err := syscall.Mkfifo(pipeFile, 0600)
	if err != nil {
		return pipeFile, err
	}

	fmt.Println("TEST - makeBootstrapPipe() called, pipe file: %s", pipeFile)
	// Get our own executable path.
	execPath, err := os.Executable()
	fmt.Println("TEST - 111 () called, execPath: %s", execPath)
	if err != nil {
		return pipeFile, err
	}

	fmt.Println("TEST - 112 () ")
	if testSelfExecOverride != "" {
		fmt.Println("TEST - 113 () %s", testSelfExecOverride)
		execPath = testSelfExecOverride
	} else if strings.HasSuffix(execPath, "/envoy.test") {
		return pipeFile, fmt.Errorf("I seem to be running in a test binary without " +
			"overriding the self-executable. Not doing that - it will make you sad. " +
			"See testSelfExecOverride.")
	}

	// Exec the pipe-bootstrap internal sub-command which will write the bootstrap
	// from STDIN to the named pipe (once Envoy opens it) and then clean up the
	// file for us.
	cmd := exec.Command(execPath, "connect", "envoy", "pipe-bootstrap", pipeFile)
	fmt.Println("TEST - 114 () %s", cmd)

	stdin, err := cmd.StdinPipe()
	fmt.Println("TEST - 115 () ")

	if err != nil {
		fmt.Println("TEST - 116 () ")
		return pipeFile, err
	}

	// Write the config
	fmt.Println("TEST - 117 () ")
	fmt.Println("bootstrapJSON Size: -> ", len(bootstrapJSON))
	fmt.Println("bootstrapJSON string: -> ", string(bootstrapJSON))
	n, err := stdin.Write(bootstrapJSON)
	fmt.Println("TEST - 118 () ")
	// Close STDIN whether it was successful or not
	stdin.Close()
	fmt.Println("TEST - 119 () ")
	if err != nil {
		fmt.Println("TEST - 120 () ")
		return pipeFile, err
	}

	fmt.Println("TEST - 121 () ")
	if n < len(bootstrapJSON) {
		fmt.Println("TEST - 122 () ")
		return pipeFile, fmt.Errorf("failed writing boostrap to child STDIN: %s", err)
	}

	fmt.Println("TEST - 123 () ")
	err = cmd.Start()

	fmt.Println("TEST - 124 () ")
	if err != nil {
		fmt.Println("TEST - 125 () ")
		return pipeFile, err
	}

	fmt.Println("TEST - 123 () ")
	fmt.Println("TEST - successfully write to Bootstrap pipe file: %s", pipeFile)
	// We can't wait for the process since we need to exec into Envoy before it
	// will be able to complete so it will be remain as a zombie until Envoy is
	// killed then will be reaped by the init process (pid 0). This is all a bit
	// gross but the cleanest workaround I can think of for Envoy 1.10 not
	// supporting /dev/fd/<fd> config paths any more. So we are done and leaving
	// the child to run it's course without reaping it.
	return pipeFile, nil
}

func execEnvoy(binary string, prefixArgs, suffixArgs []string, bootstrapJSON []byte) error {
	fmt.Println("TEST - execEnvoy() called")
	pipeFile, err := makeBootstrapPipe(bootstrapJSON)
	if err != nil {
		os.RemoveAll(pipeFile)
		return err
	}
	// We don't defer a cleanup since we are about to Exec into Envoy which means
	// defer will never fire. The child process cleans up for us in the happy
	// path.

	// We default to disabling hot restart because it makes it easier to run
	// multiple envoys locally for testing without them trying to share memory and
	// unix sockets and complain about being different IDs. But if user is
	// actually configuring hot-restart explicitly with the --restart-epoch option
	// then don't disable it!
	disableHotRestart := !hasHotRestartOption(prefixArgs, suffixArgs)

	// First argument needs to be the executable name.
	envoyArgs := []string{binary}
	envoyArgs = append(envoyArgs, prefixArgs...)
	envoyArgs = append(envoyArgs, "--config-path", pipeFile)
	if disableHotRestart {
		envoyArgs = append(envoyArgs, "--disable-hot-restart")
	}
	if !hasMaxObjNameLenOption(prefixArgs, suffixArgs) {
		envoyArgs = append(envoyArgs, "--max-obj-name-len", "256")
	}
	envoyArgs = append(envoyArgs, suffixArgs...)

	fmt.Println("TEST -3- Starting envoy with args: ", envoyArgs)
	// Exec
	if err = unix.Exec(binary, envoyArgs, os.Environ()); err != nil {
		return errors.New("Failed to exec envoy: " + err.Error())
	}

	return nil
}
