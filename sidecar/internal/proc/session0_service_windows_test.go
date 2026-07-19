//go:build windows

package proc

import (
	"net"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestRunnerPipeRejectsRemoteClients(t *testing.T) {
	if runnerPipeMode&windows.PIPE_REJECT_REMOTE_CLIENTS == 0 {
		t.Fatal("runner pipe permits remote clients")
	}
}

func TestRelayRunnerConnectionIsBidirectionalAndClosesWorker(t *testing.T) {
	client, serviceClient := net.Pipe()
	serviceWorker, worker := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- relayRunnerConnection(serviceClient, serviceWorker) }()

	request := runnerFrame{Type: "ctrl-c"}
	go func() { _ = writeRunnerFrame(client, request) }()
	gotRequest, err := readRunnerFrame(worker)
	if err != nil {
		t.Fatal(err)
	}
	if gotRequest.Type != request.Type {
		t.Fatalf("worker got %#v", gotRequest)
	}

	reply := runnerFrame{Type: "output", Data: []byte("reply")}
	go func() { _ = writeRunnerFrame(worker, reply) }()
	gotReply, err := readRunnerFrame(client)
	if err != nil {
		t.Fatal(err)
	}
	if gotReply.Type != reply.Type || string(gotReply.Data) != string(reply.Data) {
		t.Fatalf("client got %#v", gotReply)
	}

	_ = client.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("relay: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("relay did not stop after client disconnect")
	}

	if _, err := worker.Write([]byte("after-close")); err == nil {
		t.Fatal("worker connection remained open after client disconnect")
	}
	_ = worker.Close()
}
