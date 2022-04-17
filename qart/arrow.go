// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm

package main

import (
	"image"
	"image/color"
	"image/draw"
)

// Arrow handles a request for an arrow pointing in a given direction.
func Arrow(size, dir int) []byte {
	if size == 0 {
		size = 50
	}
	del := size / 10

	m := image.NewRGBA(image.Rect(0, 0, size, size))

	if dir == 4 {
		draw.Draw(m, m.Bounds(), image.Black, image.ZP, draw.Src)
		draw.Draw(m, image.Rect(5, 5, size-5, size-5), image.White, image.ZP, draw.Src)
	}

	pt := func(x, y int, c color.RGBA) {
		switch dir {
		case 0:
			m.SetRGBA(x, y, c)
		case 1:
			m.SetRGBA(y, size-1-x, c)
		case 2:
			m.SetRGBA(size-1-x, size-1-y, c)
		case 3:
			m.SetRGBA(size-1-y, x, c)
		}
	}

	for y := 0; y < size/2; y++ {
		for x := 0; x < del && x < y; x++ {
			pt(x, y, color.RGBA{0, 0, 0, 255})
		}
		for x := del; x < y-del; x++ {
			pt(x, y, color.RGBA{128, 128, 255, 255})
		}
		for x := max(y-del, 0); x <= y; x++ {
			pt(x, y, color.RGBA{0, 0, 0, 255})
		}
	}
	for y := size / 2; y < size; y++ {
		for x := 0; x < del && x < size-1-y; x++ {
			pt(x, y, color.RGBA{0, 0, 0, 255})
		}
		for x := del; x < size-1-y-del; x++ {
			pt(x, y, color.RGBA{128, 128, 192, 255})
		}
		for x := max(size-1-y-del, 0); x <= size-1-y; x++ {
			pt(x, y, color.RGBA{0, 0, 0, 255})
		}
	}
	return pngEncode(m)
}

func max(x, y int) int {
	if x > y {
		return x
	}
	return y
}
