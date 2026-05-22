package main

import (
	"bufio"
	"encoding/json"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"clipstack/internal/clipboard"
	"clipstack/internal/db"
	"clipstack/internal/proto"
)

const (
	socketPath   = "/tmp/clipstack.sock"
	pollInterval = 500 * time.Millisecond
	maxItemLen   = 1024 * 64
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		os.Exit(1)
	}

	dataDir := filepath.Join(home, ".local", "share", "clipstack")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		os.Exit(1)
	}

	logFile, err := os.OpenFile(
		filepath.Join(dataDir, "daemon.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600,
	)
	if err != nil {
		os.Exit(1)
	}
	defer logFile.Close()
	log.SetOutput(logFile)
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	database, err := db.Open()
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer database.Close()

	os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		ln.Close()
		os.Remove(socketPath)
		os.Exit(0)
	}()

	log.Println("clipd started")
	go pollClipboard(database)

	for {
		conn, err := ln.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "use of closed network connection") {
				return
			}
			log.Printf("accept: %v", err)
			continue
		}
		go handleConn(conn, database)
	}
}

func pollClipboard(database *db.DB) {
	var last string
	for {
		time.Sleep(pollInterval)
		text, err := clipboard.Read()
		if err != nil {
			continue
		}
		if text == "" || text == last {
			continue
		}
		if len(text) > maxItemLen {
			continue
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		last = text
		if err := database.Insert(text); err != nil {
			log.Printf("insert: %v", err)
		}
	}
}

func handleConn(conn net.Conn, database *db.DB) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		var req proto.Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			sendErr(conn, "bad request")
			continue
		}

		limit := req.Limit
		if limit == 0 {
			limit = 200
		}

		switch req.Type {
		case proto.MsgList:
			items, err := database.List(limit, req.Offset)
			if err != nil {
				sendErr(conn, err.Error())
			} else {
				sendResp(conn, items)
			}

		case proto.MsgSearch:
			items, err := database.Search(req.Query, limit, req.Offset)
			if err != nil {
				sendErr(conn, err.Error())
			} else {
				sendResp(conn, items)
			}

		case proto.MsgPin:
			if err := database.SetPinned(req.ID, true); err != nil {
				sendErr(conn, err.Error())
			} else {
				sendResp(conn, nil)
			}

		case proto.MsgUnpin:
			if err := database.SetPinned(req.ID, false); err != nil {
				sendErr(conn, err.Error())
			} else {
				sendResp(conn, nil)
			}

		case proto.MsgDelete:
			if err := database.Delete(req.ID); err != nil {
				sendErr(conn, err.Error())
			} else {
				sendResp(conn, nil)
			}

		case proto.MsgCopy:
			content, err := database.GetContent(req.ID)
			if err != nil {
				sendErr(conn, err.Error())
				continue
			}
			if err := clipboard.Write(content); err != nil {
				sendErr(conn, err.Error())
			} else {
				sendResp(conn, nil)
			}

		default:
			sendErr(conn, "unknown type")
		}
	}
}

func sendResp(conn net.Conn, items []proto.Item) {
	resp := proto.Response{Type: proto.MsgResp, Items: items}
	b, err := proto.Encode(resp)
	if err != nil {
		return
	}
	conn.Write(b)
}

func sendErr(conn net.Conn, msg string) {
	resp := proto.Response{Type: proto.MsgErr, Error: msg}
	b, err := proto.Encode(resp)
	if err != nil {
		return
	}
	conn.Write(b)
}
