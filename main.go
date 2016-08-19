// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build h2demo

package main

import (
    "crypto/tls"
    "flag"
    "fmt"
    "io"
    "log"
    "net"
    "net/http"
    "strings"
    "sync"
    "time"

    "golang.org/x/net/http2"
)

var (
    prod = flag.Bool("prod", false, "Whether to configure itself to be the production http2.golang.org server.")
)


func clockStreamHandler(w http.ResponseWriter, r *http.Request) {
    clientGone := w.(http.CloseNotifier).CloseNotify()

    w.Header().Set("Content-Type", "text/plain")
    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()
    fmt.Fprintf(w, "# ~1KB of junk to force browsers to start rendering immediately: \n")
    io.WriteString(w, strings.Repeat("# xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n", 13))

    for {
        fmt.Fprintf(w, "%v\n", time.Now())
        w.(http.Flusher).Flush()
        select {
        case <-ticker.C:
        case <-clientGone:
            log.Printf("Client %v disconnected from the clock", r.RemoteAddr)
            return
        }
    }
}

func registerHandlers() {

    mux2 := http.NewServeMux()
    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        if r.TLS == nil {
            return
        }
        if r.ProtoMajor == 1 {
            return
        }
        mux2.ServeHTTP(w, r)
    })
    
    mux2.HandleFunc("/clockstream", clockStreamHandler)

}

func serveProdTLS() error {

    cert, err := tls.LoadX509KeyPair("keys/rootCA.pem", "keys/rootCA.key")
    if err != nil {
        return err
    }
    srv := &http.Server{
        TLSConfig: &tls.Config{
            Certificates: []tls.Certificate{cert},
        },
    }

    http2.ConfigureServer(srv, &http2.Server{})
    ln, err := net.Listen("tcp", ":4430")
    if err != nil {
        return err
    }

    url := "https://127.0.0.1:4430/"
    log.Printf("Listening on " + url)
    log.Printf("Path:" + url+ "clockstream")
    return srv.Serve(tls.NewListener(tcpKeepAliveListener{ln.(*net.TCPListener)}, srv.TLSConfig))
}

type tcpKeepAliveListener struct {
    *net.TCPListener
}

/* 设置heartbeats 解决掉网问题 */

func (ln tcpKeepAliveListener) Accept() (c net.Conn, err error) {
    tc, err := ln.AcceptTCP()
    if err != nil {
        return
    }
    tc.SetKeepAlive(true)
    tc.SetKeepAlivePeriod(3 * time.Minute)
    // tc.SetKeepAliveIdle(60 * time.Second)
    // tc.SetKeepAliveCount(4)
    // tc.SetKeepAliveInterval(12 * time.Second)
    // tc.SetNoDelay(true)
    return tc, nil
}

func serveProd() error {
    errc := make(chan error, 1)
    go func() { errc <- serveProdTLS() }()
    return <-errc
}

const idleTimeout = 5 * time.Minute
const activeTimeout = 10 * time.Minute

// TODO: put this into the standard library and actually send
// PING frames and GOAWAY, etc: golang.org/issue/14204
func idleTimeoutHook() func(net.Conn, http.ConnState) {
    var mu sync.Mutex
    m := map[net.Conn]*time.Timer{}
    return func(c net.Conn, cs http.ConnState) {
        mu.Lock()
        defer mu.Unlock()
        if t, ok := m[c]; ok {
            delete(m, c)
            t.Stop()
        }
        var d time.Duration
        switch cs {
        case http.StateNew, http.StateIdle:
            d = idleTimeout
        case http.StateActive:
            d = activeTimeout
        default:
            return
        }
        m[c] = time.AfterFunc(d, func() {
            log.Printf("closing idle conn %v after %v", c.RemoteAddr(), d)
            go c.Close()
        })
    }
}

func main() {

    var srv http.Server
    flag.BoolVar(&http2.VerboseLogs, "verbose", false, "Verbose HTTP/2 debugging.")
    flag.Parse()
    srv.Addr = "127.0.0.1:4430"
    srv.ConnState = idleTimeoutHook()

    registerHandlers()
    log.Fatal(serveProd())

    select {}
}

