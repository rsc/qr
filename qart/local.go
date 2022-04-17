// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// "go run local.go" to get the live development environment on localhost:8080.

//go:build ignore

package main

import (
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
)

func main() {
	http.Handle("/", http.FileServer(http.Dir(".")))
	http.Handle("/wasm_exec.js", http.FileServer(http.Dir(runtime.GOROOT()+"/misc/wasm/")))
	http.HandleFunc("/main.wasm", func(w http.ResponseWriter, r *http.Request) {
		cmd := exec.Command("go", "build", "-o", "_live.wasm")
		cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
		if out, err := cmd.CombinedOutput(); err != nil {
			log.Printf("go build: %s\n%s", err, out)
			return
		}
		data, err := os.ReadFile("_live.wasm")
		if err != nil {
			log.Print(err)
			return
		}
		w.Write(data)
		os.Remove("_live.wasm")
	})
	log.Fatal(http.ListenAndServe(":8080", nil))
}
