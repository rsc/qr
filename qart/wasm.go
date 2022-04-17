// Copyright 2012 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm

// Qart is a WebAssembly program to help create artistic QR code images.
// The algorithms are described at https://research.swtch.com/qart,
// and this program is running at https://research.swtch.com/qr/draw/.
//
// To run the program locally, use “go run local.go”.
package main

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"fmt"
	"html"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"strings"
	"syscall/js"
)

//go:embed pjw.png
var pjwPNG []byte

var (
	doc js.Value // JS document

	// checkboxes
	checkRand    js.Value
	checkData    js.Value
	checkDither  js.Value
	checkControl js.Value

	inputURL js.Value // url box
)

var pic = &Image{
	File:    pjwPNG,
	Dx:      4,
	Dy:      4,
	URL:     "https://research.swtch.com/qart",
	Version: 6,
	Mask:    2,
}

func up()       { pic.Dy++ }
func down()     { pic.Dy-- }
func left()     { pic.Dx++ }
func right()    { pic.Dx-- }
func ibigger()  { pic.Size++ }
func ismaller() { pic.Size-- }
func rotate()   { pic.Rotation = (pic.Rotation + 1) & 3 }

func bigger() {
	if pic.Version < 8 {
		pic.Version++
	}
}

func smaller() {
	if pic.Version > 1 {
		pic.Version--
	}
}

func setImage(id string, img []byte) {
	doc.Call("getElementById", id).Set("src", "data:image/png;base64,"+base64.StdEncoding.EncodeToString(img))
}

func setErr(err error) {
	doc.Call("getElementById", "err-output").Set("innerHTML", html.EscapeString(err.Error()))
}

func update() {
	pic.Rand = checkRand.Get("checked").Bool()
	pic.OnlyDataBits = checkData.Get("checked").Bool()
	pic.Dither = checkDither.Get("checked").Bool()
	pic.SaveControl = checkControl.Get("checked").Bool()
	pic.URL = inputURL.Get("value").String()
	img, err := pic.Encode()
	setImage("img-output", img)
	doc.Call("getElementById", "img-download").Set("href", "data:image/png;base64,"+base64.StdEncoding.EncodeToString(img))
	if err != nil {
		setErr(err)
	}
}

func funcOf(f func()) js.Func {
	return js.FuncOf(func(_ js.Value, _ []js.Value) any {
		f()
		return nil
	})
}

func main() {
	doc = js.Global().Get("document")
	checkRand = doc.Call("getElementById", "rand")
	checkData = doc.Call("getElementById", "data")
	checkDither = doc.Call("getElementById", "dither")
	checkControl = doc.Call("getElementById", "control")
	inputURL = doc.Call("getElementById", "url")

	setImage("arrow-right", Arrow(48, 0))
	setImage("arrow-up", Arrow(48, 1))
	setImage("arrow-left", Arrow(48, 2))
	setImage("arrow-down", Arrow(48, 3))

	setImage("arrow-smaller", Arrow(20, 2))
	setImage("arrow-bigger", Arrow(20, 0))

	setImage("arrow-ismaller", Arrow(20, 2))
	setImage("arrow-ibigger", Arrow(20, 0))

	update()

	doc.Call("getElementById", "loading").Get("style").Set("display", "none")
	doc.Call("getElementById", "wasm1").Get("style").Set("display", "block")
	doc.Call("getElementById", "wasm2").Get("style").Set("display", "block")

	if img, err := pic.Src(); err == nil {
		setImage("img-src", img)
	} else {
		setErr(err)
	}

	do := func(id string, f func()) {
		doc.Call("getElementById", id).Set("onclick", funcOf(func() { f(); update() }))
	}
	do("left", left)
	do("right", right)
	do("up", up)
	do("down", down)
	do("smaller", smaller)
	do("bigger", bigger)
	do("ismaller", ismaller)
	do("ibigger", ibigger)
	do("rotate", rotate)

	updateJS := funcOf(update)
	for _, id := range []string{"rand", "data", "dither", "control", "redraw"} {
		doc.Call("getElementById", id).Set("onclick", updateJS)
	}
	inputURL.Call("addEventListener", "change", updateJS)

	fmt.Println("hello")
	doc.Call("getElementById", "upload-input").Call("addEventListener", "change",
		js.FuncOf(func(this js.Value, args []js.Value) any {
			fmt.Println("newfile")
			files := this.Get("files")
			if files.Get("length").Int() != 1 {
				return nil
			}
			r := js.Global().Get("FileReader").New()
			var cb js.Func
			cb = js.FuncOf(func(this js.Value, args []js.Value) any {
				_, enc, _ := strings.Cut(r.Get("result").String(), ";base64,")
				fmt.Printf("%q\n", enc)
				data, err := base64.StdEncoding.DecodeString(enc)
				defer cb.Release()
				if err != nil {
					setErr(err)
					return nil
				}
				fmt.Println(len(data))
				fmt.Printf("%q\n", data[:20])

				_, _, err = image.Decode(bytes.NewReader(data))
				if err != nil {
					setErr(err)
					return nil
				}
				pic.SetFile(data)
				img, err := pic.Src()
				if err != nil {
					setErr(err)
					return nil
				}
				setImage("img-src", img)
				update()
				return nil
			})
			r.Call("addEventListener", "load", cb)
			r.Call("readAsDataURL", files.Index(0))
			return nil
		}))

	select {}
}
