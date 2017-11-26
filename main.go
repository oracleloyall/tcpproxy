package main

import (
	"flag"
	"log"
	"net"
	"os"
	"syscall"
)

const (
	SPLICE_F_MOVE     = 1
	SPLICE_F_NONBLOCK = 2
)

var (
	pipesize int
)

func splicefrom(from syscall.RawConn, pfd int, maxsize int) (int, error) {
	nsplice := 0
	var lasterr error
	err := from.Read(func(fd uintptr) bool {
		for nsplice < maxsize {
			n, err := syscall.Splice(int(fd), nil, pfd, nil, maxsize-nsplice, SPLICE_F_MOVE|SPLICE_F_NONBLOCK)
			if err != nil {
				if en, ok := err.(syscall.Errno); !ok || !en.Timeout() {
					lasterr = err
				}
				break
			} else if n == 0 {
				return true
			} else {
				nsplice += int(n)
			}
		}
		return nsplice > 0 || nsplice == maxsize || lasterr != nil
	})
	if lasterr == nil {
		lasterr = err
	}
	return nsplice, lasterr
}

func spliceto(pfd int, to syscall.RawConn, maxsize int) (int, error) {
	nsplice := 0
	var lasterr error
	err := to.Write(func(fd uintptr) bool {
		for nsplice < maxsize {
			n, err := syscall.Splice(pfd, nil, int(fd), nil, maxsize-nsplice, SPLICE_F_MOVE|SPLICE_F_NONBLOCK)
			if err != nil {
				if en, ok := err.(syscall.Errno); !ok || !en.Timeout() {
					lasterr = err
				}
				break
			} else if n == 0 {
				return true
			} else if n > 0 {
				nsplice += int(n)
			}
		}
		return nsplice == maxsize || lasterr != nil
	})
	if lasterr == nil {
		lasterr = err
	}
	return nsplice, lasterr
}

func splice(from *net.TCPConn, to *net.TCPConn) {
	defer from.Close()
	defer to.Close()
	fromrc, err := from.SyscallConn()
	if err != nil {
		log.Printf("from.SyscallConn: %v", err)
		return
	}
	torc, err := to.SyscallConn()
	if err != nil {
		log.Printf("to.SyscallConn: %v", err)
		return
	}
	var pfd [2]int
	err = syscall.Pipe2(pfd[:], syscall.O_CLOEXEC)
	if err != nil {
		log.Printf("syscall.Pipe2: %v", err)
		return
	}
	for {
		n, err := splicefrom(fromrc, pfd[1], pipesize)
		if err != nil {
			log.Printf("splicefrom: %v", err)
			break
		} else if n == 0 {
			log.Printf("splicefrom: EOF")
			break
		}
		_, err = spliceto(pfd[0], torc, n)
		if err != nil {
			log.Printf("splicefrom: %v", err)
			break
		}
	}
	syscall.Close(pfd[0])
	syscall.Close(pfd[1])
}

func handleconn(fc *net.TCPConn, backaddr string) {
	ba, err := net.ResolveTCPAddr("tcp", backaddr)
	if err != nil {
		log.Printf("net.ResolveTCPAddr: %v", err)
		fc.Close()
		return
	}
	bc, err := net.DialTCP("tcp", nil, ba)
	if err != nil {
		log.Printf("net.DialTCP: %v", err)
		fc.Close()
		return
	}
	go splice(bc, fc)
	splice(fc, bc)
}

func initpipesize() error {
	var pfd [2]int
	err := syscall.Pipe(pfd[:])
	if err != nil {
		return err
	}
	r, _, en := syscall.Syscall(syscall.SYS_FCNTL, uintptr(pfd[0]), syscall.F_GETPIPE_SZ, 0)
	pipesize = int(r)
	syscall.Close(pfd[0])
	syscall.Close(pfd[1])
	if en != 0 {
		return en
	}
	return nil
}

func main() {
	var (
		frontaddr string
		backaddr  string
	)
	flag.StringVar(&frontaddr, "front", ":9999", "front address")
	flag.StringVar(&backaddr, "back", "127.0.0.1:8888", "back address")
	flag.Parse()

	if frontaddr == "" || backaddr == "" {
		flag.Usage()
		os.Exit(1)
	}
	la, err := net.ResolveTCPAddr("tcp", frontaddr)
	if err != nil {
		log.Fatalf("net.ResolveTCPAddr: %v", err)
	}

	err = initpipesize()
	if err != nil {
		log.Fatalf("initpipesize: %v", err)
	}

	l, err := net.ListenTCP("tcp", la)
	if err != nil {
		log.Fatalf("net.ListenTCP: %v", err)
	}
	for {
		conn, err := l.AcceptTCP()
		if err != nil {
			log.Fatalf("l.AcceptTCP: %v", err)
		}
		go handleconn(conn, backaddr)
	}
}
