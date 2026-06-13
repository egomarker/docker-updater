package runner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

type Result struct {
	ExitCode int
	Output   string
}

func Run(ctx context.Context, cwd string, argv []string, logf func(string)) (*Result, error) {
	if len(argv) == 0 {
		return &Result{ExitCode: -1}, fmt.Errorf("empty argv")
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = cwd

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return &Result{ExitCode: -1}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return &Result{ExitCode: -1}, err
	}

	if err := cmd.Start(); err != nil {
		return &Result{ExitCode: -1}, err
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go streamPipe(stdout, logf, &wg)
	go streamPipe(stderr, logf, &wg)

	waitErr := cmd.Wait()
	wg.Wait()

	return &Result{ExitCode: ExitCode(waitErr)}, waitErr
}

func Capture(ctx context.Context, cwd string, argv []string, logf func(string)) (*Result, error) {
	if len(argv) == 0 {
		return &Result{ExitCode: -1}, fmt.Errorf("empty argv")
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = cwd
	output, err := cmd.CombinedOutput()
	trimmed := strings.TrimRight(string(output), "\n")
	for _, line := range splitLines(trimmed) {
		if logf != nil {
			logf(line)
		}
	}
	return &Result{ExitCode: ExitCode(err), Output: strings.TrimSpace(trimmed)}, err
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func FormatArgv(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, arg := range argv {
		if arg == "" || strings.ContainsAny(arg, " \t\n\"'") {
			parts = append(parts, strconv.Quote(arg))
			continue
		}
		parts = append(parts, arg)
	}
	return strings.Join(parts, " ")
}

func streamPipe(reader io.Reader, logf func(string), wg *sync.WaitGroup) {
	defer wg.Done()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if logf != nil {
			logf(scanner.Text())
		}
	}
	if err := scanner.Err(); err != nil && logf != nil {
		logf("output scan error: " + err.Error())
	}
}

func splitLines(value string) []string {
	if value == "" {
		return nil
	}
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.Split(value, "\n")
}
