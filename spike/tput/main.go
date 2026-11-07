// Command tput measures TCP throughput: "tput server :5301" on one side,
// "tput client <host:port> <seconds>" on the other. Spike only.
package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: tput server <addr> | tput client <addr> <seconds>")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "server":
		serve(os.Args[2])
	case "client":
		seconds, _ := strconv.Atoi(os.Args[3])
		send(os.Args[2], time.Duration(seconds)*time.Second)
	}
}

func serve(addr string) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		panic(err)
	}
	for {
		conn, err := listener.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer conn.Close()
			received, _ := io.Copy(io.Discard, conn)
			fmt.Fprintf(conn, "%d\n", received)
		}()
	}
}

func send(addr string, duration time.Duration) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		panic(err)
	}
	buffer := make([]byte, 256*1024)
	start := time.Now()
	var sent int64
	for time.Since(start) < duration {
		written, err := conn.Write(buffer)
		sent += int64(written)
		if err != nil {
			panic(err)
		}
	}
	elapsed := time.Since(start).Seconds()
	conn.(*net.TCPConn).CloseWrite()
	fmt.Printf("sent %d bytes in %.1f s: %.0f Mbit/s\n", sent, elapsed, float64(sent)*8/elapsed/1e6)
}
