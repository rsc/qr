// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm

package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math/rand"
	"sort"
	"time"

	"rsc.io/qr"
	"rsc.io/qr/coding"
	"rsc.io/qr/gf256"
	"rsc.io/qr/qart/internal/resize"
)

type Image struct {
	File  []byte
	Img48 []byte

	Target   [][]int
	Dx       int
	Dy       int
	URL      string
	Version  int
	Mask     int
	Scale    int
	Rotation int
	Size     int

	// Rand says to pick the pixels randomly.
	Rand bool

	// Dither says to dither instead of using threshold pixel layout.
	Dither bool

	// OnlyDataBits says to use only data bits, not check bits.
	OnlyDataBits bool

	// Control is a PNG showing the pixels that we controlled.
	// Pixels we don't control are grayed out.
	SaveControl bool
	Control     []byte

	// Code is the final QR code.
	Code *qr.Code
}

func (m *Image) SetFile(data []byte) {
	m.File = data
	m.Img48 = nil
	m.Target = nil
}

func (m *Image) Small() bool {
	return 8*(17+4*int(m.Version)) < 512
}

func (m *Image) Clamp() {
	if m.Version > 8 {
		m.Version = 8
	}
	if m.Scale == 0 {
		m.Scale = 8
	}
	if m.Version >= 12 && m.Scale >= 4 {
		m.Scale /= 2
	}
}

func (m *Image) Src() ([]byte, error) {
	if m.Img48 == nil {
		i, err := decode(m.File, 48)
		if err != nil {
			return nil, err
		}
		m.Img48 = pngEncode(i)
	}
	return m.Img48, nil
}

type Pixinfo struct {
	X        int
	Y        int
	Pix      coding.Pixel
	Targ     byte
	DTarg    int
	Contrast int
	HardZero bool
	Block    *BitBlock
	Bit      uint
}

type Pixorder struct {
	Off      int
	Priority int
}

type byPriority []Pixorder

func (x byPriority) Len() int           { return len(x) }
func (x byPriority) Swap(i, j int)      { x[i], x[j] = x[j], x[i] }
func (x byPriority) Less(i, j int) bool { return x[i].Priority > x[j].Priority }

func (m *Image) target(x, y int) (targ byte, contrast int) {
	tx := x + m.Dx
	ty := y + m.Dy
	if ty < 0 || ty >= len(m.Target) || tx < 0 || tx >= len(m.Target[ty]) {
		return 255, -1
	}

	v0 := m.Target[ty][tx]
	if v0 < 0 {
		return 255, -1
	}
	targ = byte(v0)

	n := 0
	sum := 0
	sumsq := 0
	const del = 5
	for dy := -del; dy <= del; dy++ {
		for dx := -del; dx <= del; dx++ {
			if 0 <= ty+dy && ty+dy < len(m.Target) && 0 <= tx+dx && tx+dx < len(m.Target[ty+dy]) {
				v := m.Target[ty+dy][tx+dx]
				sum += v
				sumsq += v * v
				n++
			}
		}
	}

	avg := sum / n
	contrast = sumsq/n - avg*avg
	return
}

func (m *Image) rotate(p *coding.Plan, rot int) {
	if rot == 0 {
		return
	}

	N := len(p.Pixel)
	pix := make([][]coding.Pixel, N)
	apix := make([]coding.Pixel, N*N)
	for i := range pix {
		pix[i], apix = apix[:N], apix[N:]
	}

	switch rot {
	case 0:
		// ok
	case 1:
		for y := 0; y < N; y++ {
			for x := 0; x < N; x++ {
				pix[y][x] = p.Pixel[x][N-1-y]
			}
		}
	case 2:
		for y := 0; y < N; y++ {
			for x := 0; x < N; x++ {
				pix[y][x] = p.Pixel[N-1-y][N-1-x]
			}
		}
	case 3:
		for y := 0; y < N; y++ {
			for x := 0; x < N; x++ {
				pix[y][x] = p.Pixel[N-1-x][y]
			}
		}
	}

	p.Pixel = pix
}

func (m *Image) Encode() ([]byte, error) {
	m.Clamp()
	dt := 17 + 4*m.Version + m.Size
	if len(m.Target) != dt {
		t, err := makeTarg(m.File, dt)
		if err != nil {
			return nil, err
		}
		m.Target = t
	}
	p, err := coding.NewPlan(coding.Version(m.Version), coding.L, coding.Mask(m.Mask))
	if err != nil {
		return nil, err
	}

	m.rotate(p, m.Rotation)

	rand := rand.New(rand.NewSource(time.Now().UnixNano()))

	// QR parameters.
	nd := p.DataBytes / p.Blocks
	nc := p.CheckBytes / p.Blocks
	extra := p.DataBytes - nd*p.Blocks
	rs := gf256.NewRSEncoder(coding.Field, nc)

	// Build information about pixels, indexed by data/check bit number.
	pixByOff := make([]Pixinfo, (p.DataBytes+p.CheckBytes)*8)
	expect := make([][]bool, len(p.Pixel))
	for y, row := range p.Pixel {
		expect[y] = make([]bool, len(row))
		for x, pix := range row {
			targ, contrast := m.target(x, y)
			if m.Rand && contrast >= 0 {
				contrast = rand.Intn(128) + 64*((x+y)%2) + 64*((x+y)%3%2)
			}
			expect[y][x] = pix&coding.Black != 0
			if r := pix.Role(); r == coding.Data || r == coding.Check {
				pixByOff[pix.Offset()] = Pixinfo{X: x, Y: y, Pix: pix, Targ: targ, Contrast: contrast}
			}
		}
	}

Again:
	// Count fixed initial data bits, prepare template URL.
	url := m.URL + "#"
	var b coding.Bits
	coding.String(url).Encode(&b, p.Version)
	coding.Num("").Encode(&b, p.Version)
	bbit := b.Bits()
	dbit := p.DataBytes*8 - bbit
	if dbit < 0 {
		return nil, fmt.Errorf("cannot encode URL into available bits")
	}
	num := make([]byte, dbit/10*3)
	for i := range num {
		num[i] = '0'
	}
	b.Pad(dbit)
	b.Reset()
	coding.String(url).Encode(&b, p.Version)
	coding.Num(num).Encode(&b, p.Version)
	b.AddCheckBytes(p.Version, p.Level)
	data := b.Bytes()

	doff := 0 // data offset
	coff := 0 // checksum offset
	mbit := bbit + dbit/10*10

	// Choose pixels.
	bitblocks := make([]*BitBlock, p.Blocks)
	for blocknum := 0; blocknum < p.Blocks; blocknum++ {
		if blocknum == p.Blocks-extra {
			nd++
		}

		bdata := data[doff/8 : doff/8+nd]
		cdata := data[p.DataBytes+coff/8 : p.DataBytes+coff/8+nc]
		bb := newBlock(nd, nc, rs, bdata, cdata)
		bitblocks[blocknum] = bb

		// Determine which bits in this block we can try to edit.
		lo, hi := 0, nd*8
		if lo < bbit-doff {
			lo = bbit - doff
			if lo > hi {
				lo = hi
			}
		}
		if hi > mbit-doff {
			hi = mbit - doff
			if hi < lo {
				hi = lo
			}
		}

		// Preserve [0, lo) and [hi, nd*8).
		for i := 0; i < lo; i++ {
			if !bb.canSet(uint(i), (bdata[i/8]>>uint(7-i&7))&1) {
				return nil, fmt.Errorf("cannot preserve required bits")
			}
		}
		for i := hi; i < nd*8; i++ {
			if !bb.canSet(uint(i), (bdata[i/8]>>uint(7-i&7))&1) {
				return nil, fmt.Errorf("cannot preserve required bits")
			}
		}

		// Can edit [lo, hi) and checksum bits to hit target.
		// Determine which ones to try first.
		order := make([]Pixorder, (hi-lo)+nc*8)
		for i := lo; i < hi; i++ {
			order[i-lo].Off = doff + i
		}
		for i := 0; i < nc*8; i++ {
			order[hi-lo+i].Off = p.DataBytes*8 + coff + i
		}
		if m.OnlyDataBits {
			order = order[:hi-lo]
		}
		for i := range order {
			po := &order[i]
			po.Priority = pixByOff[po.Off].Contrast<<8 | rand.Intn(256)
		}
		sort.Sort(byPriority(order))

		const mark = false
		for i := range order {
			po := &order[i]
			pinfo := &pixByOff[po.Off]
			bval := pinfo.Targ
			if bval < 128 {
				bval = 1
			} else {
				bval = 0
			}
			pix := pinfo.Pix
			if pix&coding.Invert != 0 {
				bval ^= 1
			}
			if pinfo.HardZero {
				bval = 0
			}

			var bi int
			if pix.Role() == coding.Data {
				bi = po.Off - doff
			} else {
				bi = po.Off - p.DataBytes*8 - coff + nd*8
			}
			if bb.canSet(uint(bi), bval) {
				pinfo.Block = bb
				pinfo.Bit = uint(bi)
				if mark {
					p.Pixel[pinfo.Y][pinfo.X] = coding.Black
				}
			} else {
				if pinfo.HardZero {
					panic("hard zero")
				}
				if mark {
					p.Pixel[pinfo.Y][pinfo.X] = 0
				}
			}
		}
		bb.copyOut()

		const cheat = false
		for i := 0; i < nd*8; i++ {
			pinfo := &pixByOff[doff+i]
			pix := p.Pixel[pinfo.Y][pinfo.X]
			if bb.B[i/8]&(1<<uint(7-i&7)) != 0 {
				pix ^= coding.Black
			}
			expect[pinfo.Y][pinfo.X] = pix&coding.Black != 0
			if cheat {
				p.Pixel[pinfo.Y][pinfo.X] = pix & coding.Black
			}
		}
		for i := 0; i < nc*8; i++ {
			pinfo := &pixByOff[p.DataBytes*8+coff+i]
			pix := p.Pixel[pinfo.Y][pinfo.X]
			if bb.B[nd+i/8]&(1<<uint(7-i&7)) != 0 {
				pix ^= coding.Black
			}
			expect[pinfo.Y][pinfo.X] = pix&coding.Black != 0
			if cheat {
				p.Pixel[pinfo.Y][pinfo.X] = pix & coding.Black
			}
		}
		doff += nd * 8
		coff += nc * 8
	}

	// Pass over all pixels again, dithering.
	if m.Dither {
		for i := range pixByOff {
			pinfo := &pixByOff[i]
			pinfo.DTarg = int(pinfo.Targ)
		}
		for y, row := range p.Pixel {
			for x, pix := range row {
				if pix.Role() != coding.Data && pix.Role() != coding.Check {
					continue
				}
				pinfo := &pixByOff[pix.Offset()]
				if pinfo.Block == nil {
					// did not choose this pixel
					continue
				}

				pix := pinfo.Pix

				pval := byte(1) // pixel value (black)
				v := 0          // gray value (black)
				targ := pinfo.DTarg
				if targ >= 128 {
					// want white
					pval = 0
					v = 255
				}

				bval := pval // bit value
				if pix&coding.Invert != 0 {
					bval ^= 1
				}
				if pinfo.HardZero && bval != 0 {
					bval ^= 1
					pval ^= 1
					v ^= 255
				}

				// Set pixel value as we want it.
				pinfo.Block.reset(pinfo.Bit, bval)

				_, _ = x, y

				err := targ - v
				if x+1 < len(row) {
					addDither(pixByOff, row[x+1], err*7/16)
				}
				if false && y+1 < len(p.Pixel) {
					if x > 0 {
						addDither(pixByOff, p.Pixel[y+1][x-1], err*3/16)
					}
					addDither(pixByOff, p.Pixel[y+1][x], err*5/16)
					if x+1 < len(row) {
						addDither(pixByOff, p.Pixel[y+1][x+1], err*1/16)
					}
				}
			}
		}

		for _, bb := range bitblocks {
			bb.copyOut()
		}
	}

	noops := 0
	// Copy numbers back out.
	for i := 0; i < dbit/10; i++ {
		// Pull out 10 bits.
		v := 0
		for j := 0; j < 10; j++ {
			bi := uint(bbit + 10*i + j)
			v <<= 1
			v |= int((data[bi/8] >> (7 - bi&7)) & 1)
		}
		// Turn into 3 digits.
		if v >= 1000 {
			// Oops - too many 1 bits.
			// We know the 512, 256, 128, 64, 32 bits are all set.
			// Pick one at random to clear.  This will break some
			// checksum bits, but so be it.
			pinfo := &pixByOff[bbit+10*i+3] // TODO random
			pinfo.Contrast = 1e9 >> 8
			pinfo.HardZero = true
			noops++
		}
		num[i*3+0] = byte(v/100 + '0')
		num[i*3+1] = byte(v/10%10 + '0')
		num[i*3+2] = byte(v%10 + '0')
	}
	if noops > 0 {
		goto Again
	}

	var b1 coding.Bits
	coding.String(url).Encode(&b1, p.Version)
	coding.Num(num).Encode(&b1, p.Version)
	b1.AddCheckBytes(p.Version, p.Level)
	if !bytes.Equal(b.Bytes(), b1.Bytes()) {
		fmt.Printf("mismatch\n%d %x\n%d %x\n", len(b.Bytes()), b.Bytes(), len(b1.Bytes()), b1.Bytes())
		panic("byte mismatch")
	}

	cc, err := p.Encode(coding.String(url), coding.Num(num))
	if err != nil {
		return nil, err
	}

	if !m.Dither {
		for y, row := range expect {
			for x, pix := range row {
				if cc.Black(x, y) != pix {
					println("mismatch", x, y, p.Pixel[y][x].String())
				}
			}
		}
	}

	m.Code = &qr.Code{Bitmap: cc.Bitmap, Size: cc.Size, Stride: cc.Stride, Scale: m.Scale}

	if m.SaveControl {
		m.Control = pngEncode(makeImage(0, cc.Size, 4, m.Scale, func(x, y int) (rgba uint32) {
			pix := p.Pixel[y][x]
			if pix.Role() == coding.Data || pix.Role() == coding.Check {
				pinfo := &pixByOff[pix.Offset()]
				if pinfo.Block != nil {
					if cc.Black(x, y) {
						return 0x000000ff
					}
					return 0xffffffff
				}
			}
			if cc.Black(x, y) {
				return 0x3f3f3fff
			}
			return 0xbfbfbfff
		}))
		return m.Control, nil
	}

	return m.Code.PNG(), nil
}

func addDither(pixByOff []Pixinfo, pix coding.Pixel, err int) {
	if pix.Role() != coding.Data && pix.Role() != coding.Check {
		return
	}
	pinfo := &pixByOff[pix.Offset()]
	println("add", pinfo.X, pinfo.Y, pinfo.DTarg, err)
	pinfo.DTarg += err
}

type BitBlock struct {
	DataBytes  int
	CheckBytes int
	B          []byte
	M          [][]byte
	Tmp        []byte
	RS         *gf256.RSEncoder
	bdata      []byte
	cdata      []byte
}

func newBlock(nd, nc int, rs *gf256.RSEncoder, dat, cdata []byte) *BitBlock {
	b := &BitBlock{
		DataBytes:  nd,
		CheckBytes: nc,
		B:          make([]byte, nd+nc),
		Tmp:        make([]byte, nc),
		RS:         rs,
		bdata:      dat,
		cdata:      cdata,
	}
	copy(b.B, dat)
	rs.ECC(b.B[:nd], b.B[nd:])
	b.check()
	if !bytes.Equal(b.Tmp, cdata) {
		panic("cdata")
	}

	b.M = make([][]byte, nd*8)
	for i := range b.M {
		row := make([]byte, nd+nc)
		b.M[i] = row
		for j := range row {
			row[j] = 0
		}
		row[i/8] = 1 << (7 - uint(i%8))
		rs.ECC(row[:nd], row[nd:])
	}
	return b
}

func (b *BitBlock) check() {
	b.RS.ECC(b.B[:b.DataBytes], b.Tmp)
	if !bytes.Equal(b.B[b.DataBytes:], b.Tmp) {
		fmt.Printf("ecc mismatch\n%x\n%x\n", b.B[b.DataBytes:], b.Tmp)
		panic("mismatch")
	}
}

func (b *BitBlock) reset(bi uint, bval byte) {
	if (b.B[bi/8]>>(7-bi&7))&1 == bval {
		// already has desired bit
		return
	}
	// rows that have already been set
	m := b.M[len(b.M):cap(b.M)]
	for _, row := range m {
		if row[bi/8]&(1<<(7-bi&7)) != 0 {
			// Found it.
			for j, v := range row {
				b.B[j] ^= v
			}
			return
		}
	}
	panic("reset of unset bit")
}

func (b *BitBlock) canSet(bi uint, bval byte) bool {
	found := false
	m := b.M
	for j, row := range m {
		if row[bi/8]&(1<<(7-bi&7)) == 0 {
			continue
		}
		if !found {
			found = true
			if j != 0 {
				m[0], m[j] = m[j], m[0]
			}
			continue
		}
		for k := range row {
			row[k] ^= m[0][k]
		}
	}
	if !found {
		return false
	}

	targ := m[0]

	// Subtract from saved-away rows too.
	for _, row := range m[len(m):cap(m)] {
		if row[bi/8]&(1<<(7-bi&7)) == 0 {
			continue
		}
		for k := range row {
			row[k] ^= targ[k]
		}
	}

	// Found a row with bit #bi == 1 and cut that bit from all the others.
	// Apply to data and remove from m.
	if (b.B[bi/8]>>(7-bi&7))&1 != bval {
		for j, v := range targ {
			b.B[j] ^= v
		}
	}
	b.check()
	n := len(m) - 1
	m[0], m[n] = m[n], m[0]
	b.M = m[:n]

	for _, row := range b.M {
		if row[bi/8]&(1<<(7-bi&7)) != 0 {
			panic("did not reduce")
		}
	}

	return true
}

func (b *BitBlock) copyOut() {
	b.check()
	copy(b.bdata, b.B[:b.DataBytes])
	copy(b.cdata, b.B[b.DataBytes:])
}

func decode(data []byte, max int) (*image.RGBA, error) {
	i, _, err := image.Decode(bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}
	b := i.Bounds()
	dx, dy := max, max
	if b.Dx() > b.Dy() {
		dy = b.Dy() * dx / b.Dx()
	} else {
		dx = b.Dx() * dy / b.Dy()
	}
	var irgba *image.RGBA
	switch i := i.(type) {
	default:
		irgba = resize.Resample(i, i.Bounds(), dx, dy)
	case *image.RGBA:
		irgba = resize.ResizeRGBA(i, i.Bounds(), dx, dy)
	case *image.NRGBA:
		irgba = resize.ResizeNRGBA(i, i.Bounds(), dx, dy)
	}
	return irgba, nil
}

func makeTarg(data []byte, max int) ([][]int, error) {
	i, err := decode(data, max)
	if err != nil {
		return nil, err
	}
	b := i.Bounds()
	dx, dy := b.Dx(), b.Dy()
	targ := make([][]int, dy)
	arr := make([]int, dx*dy)
	for y := 0; y < dy; y++ {
		targ[y], arr = arr[:dx], arr[dx:]
		row := targ[y]
		for x := 0; x < dx; x++ {
			p := i.Pix[y*i.Stride+4*x:]
			r, g, b, a := p[0], p[1], p[2], p[3]
			if a == 0 {
				row[x] = -1
			} else {
				row[x] = int((299*uint32(r) + 587*uint32(g) + 114*uint32(b) + 500) / 1000)
			}
		}
	}
	return targ, nil
}

func pngEncode(c image.Image) []byte {
	var b bytes.Buffer
	png.Encode(&b, c)
	return b.Bytes()
}

func makeImage(pt, size, border, scale int, f func(x, y int) uint32) *image.RGBA {
	d := (size + 2*border) * scale
	c := image.NewRGBA(image.Rect(0, 0, d, d))

	// white
	u := &image.Uniform{C: color.White}
	draw.Draw(c, c.Bounds(), u, image.ZP, draw.Src)

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			r := image.Rect((x+border)*scale, (y+border)*scale, (x+border+1)*scale, (y+border+1)*scale)
			rgba := f(x, y)
			u.C = color.RGBA{byte(rgba >> 24), byte(rgba >> 16), byte(rgba >> 8), byte(rgba)}
			draw.Draw(c, r, u, image.ZP, draw.Src)
		}
	}
	return c
}
