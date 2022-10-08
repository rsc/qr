// Copyright 2011 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package qr

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"io"
	"os"
	"testing"
)

func TestGIF(t *testing.T) {
	c, err := Encode("hello, world", L)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := encodeGIF(&buf, c.Image()); err != nil {
		t.Fatal(err)
	}
	gifdat := buf.Bytes()
	if true {
		os.WriteFile("x.gif", gifdat, 0666)
	}
	m, err := gif.Decode(bytes.NewBuffer(gifdat))
	if err != nil {
		t.Fatal(err)
	}
	gm := m.(*image.Paletted)

	scale := c.Scale
	siz := c.Size
	nbad := 0
	for y := 0; y < scale*(8+siz); y++ {
		for x := 0; x < scale*(8+siz); x++ {
			v := byte(255)
			if c.Black(x/scale-4, y/scale-4) {
				v = 0
			}
			if gv := gm.At(x, y).(color.RGBA).R; gv != v {
				t.Errorf("%d,%d = %d, want %d", x, y, gv, v)
				if nbad++; nbad >= 20 {
					t.Fatalf("too many bad pixels")
				}
			}
		}
	}
}

type bwQuantizer struct{}

func (bwQuantizer) Quantize(p color.Palette, m image.Image) color.Palette {
	if len(p) >= 2 {
		return p
	}
	return append(p[:0], color.Black, color.White)
}
func encodeGIF(w io.Writer, m image.Image) error {
	return gif.Encode(w, m, &gif.Options{NumColors: 2, Quantizer: bwQuantizer{}})
}
