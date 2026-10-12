// A real Job-owned child serves HTTP. Killing its owner kills this project,
// making installation continuity observable without using any user project.
package main

import (
	"flag"
	"golang.org/x/sys/windows"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"
	"unsafe"
)

var BuildVersion = "1"

func main() {
	ready := flag.String("ready", "", "ready file")
	legacy := flag.Bool("legacy", false, "old shutdown API")
	child := flag.Bool("child", false, "managed project")
	flag.Parse()
	if !*child {
		exe, _ := os.Executable()
		p := exec.Command(exe, "-child", "-ready", *ready+".project")
		if err := p.Start(); err != nil {
			panic(err)
		}
		job, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			panic(err)
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			panic(err)
		}
		h, err := windows.OpenProcess(windows.PROCESS_ALL_ACCESS, false, uint32(p.Process.Pid))
		if err != nil {
			panic(err)
		}
		if err := windows.AssignProcessToJobObject(job, h); err != nil {
			panic(err)
		}
		windows.CloseHandle(h)
		defer windows.CloseHandle(job)
		os.WriteFile(*ready+".project.pid", []byte(strconv.Itoa(p.Process.Pid)), 0600)
	}
	done := make(chan bool, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","version":"` + BuildVersion + `"}`))
	})
	mux.HandleFunc("/api/desktop/shutdown", func(w http.ResponseWriter, r *http.Request) {
		if *legacy {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"shuttingDown":true}`))
		os.WriteFile(*ready+".graceful", []byte("ok"), 0600)
		done <- true
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	os.WriteFile(*ready, []byte(ln.Addr().String()), 0600)
	go http.Serve(ln, mux)
	<-done
	time.Sleep(200 * time.Millisecond)
}
