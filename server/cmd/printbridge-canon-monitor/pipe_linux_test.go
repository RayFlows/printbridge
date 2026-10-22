package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestMonitorHelper(t *testing.T) {
	if os.Getenv("PRINTBRIDGE_MONITOR_HELPER") != "1" {
		return
	}
	os.Args = []string{"monitor", "1", "virtual", "fixture", "1", ""}
	main()
	os.Exit(0)
}

func TestInheritedCUPSPipes(t *testing.T) {
	back, device, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer back.Close()
	defer device.Close()
	cmd := exec.Command(os.Args[0], "-test.run=TestMonitorHelper")
	cmd.Env = append(os.Environ(), "PRINTBRIDGE_MONITOR_HELPER=1")
	cmd.ExtraFiles = []*os.File{back}
	input, _ := cmd.StdinPipe()
	output, _ := cmd.StdoutPipe()
	var log bytes.Buffer
	cmd.Stderr = &log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	_ = back.Close()
	_, _ = io.WriteString(device, `<cmd><operation>GetStatusResponse</operation><response>OK</response><status>stopped</status><status_detail>MediaEmpty</status_detail></cmd>`)
	_, _ = input.Write(bytes.Repeat([]byte{0, 255, 3}, 100))
	_ = input.Close()
	got, err := io.ReadAll(output)
	if err != nil || !bytes.Equal(got, bytes.Repeat([]byte{0, 255, 3}, 100)) {
		t.Fatalf("print stream: %d %v", len(got), err)
	}
	_ = device.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("monitor: %v %s", err, log.String())
	}
	if !bytes.Contains(log.Bytes(), []byte("STATE: +media-empty-error\n")) {
		t.Fatalf("no device state: %s", log.String())
	}
}

func TestSIGTERMInterruptsInheritedPipeRead(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestMonitorHelper")
	cmd.Env = append(os.Environ(), "PRINTBRIDGE_MONITOR_HELPER=1")
	input, _ := cmd.StdinPipe()
	defer input.Close()
	output, _ := cmd.StdoutPipe()
	var log bytes.Buffer
	cmd.Stderr = &log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	_, _ = input.Write([]byte("fixture"))
	// Observed output proves the helper has installed its signal handlers.
	if _, err := io.ReadFull(output, make([]byte, len("fixture"))); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancel: %v %s", err, log.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SIGTERM did not interrupt CUPS input")
	}
}

func TestSIGTERMInterruptsInheritedPipeWrite(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestMonitorHelper")
	cmd.Env = append(os.Environ(), "PRINTBRIDGE_MONITOR_HELPER=1")
	input, _ := cmd.StdinPipe()
	defer input.Close()
	output, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	written := make(chan struct{})
	go func() {
		_, _ = input.Write(bytes.Repeat([]byte{1}, 1<<20))
		close(written)
	}()
	if _, err := io.ReadFull(output, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancel blocked output: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SIGTERM did not interrupt CUPS output")
	}
	<-written
}
