//go:build windows

package proc

import (
	"errors"
	"io"
	"net"
	"os"
)

func relayRunnerConnection(client, worker io.ReadWriteCloser) error {
	errs := make(chan error, 2)
	relay := func(dst io.Writer, src io.Reader) {
		for {
			frame, err := readRunnerFrame(src)
			if err == nil {
				err = writeRunnerFrame(dst, frame)
			}
			if err != nil {
				errs <- err
				return
			}
		}
	}
	go relay(worker, client)
	go relay(client, worker)

	err := <-errs
	_ = client.Close()
	_ = worker.Close()
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}
