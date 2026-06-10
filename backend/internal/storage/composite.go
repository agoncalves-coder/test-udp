package storage

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Composite fusiona los frames completos de la sesión en una sola imagen más
// nítida y con menos ruido, asumiendo un sujeto aproximadamente quieto:
//
//  1. Referencia: el frame más nítido (mayor energía de gradiente — penaliza
//     motion blur y desenfoque).
//  2. Alineación: traslación entera por frame contra la referencia (búsqueda
//     SAD ±maxShift px sobre luma submuestreada; compensa el temblor de mano).
//  3. Fusión: mediana por píxel y por canal sobre el stack alineado — reduce
//     ruido ~√N como el promedio, pero rechaza outliers (parpadeos, fantasmas
//     de movimiento, bloques JPEG dañados) que un promedio arrastraría.
//
// Devuelve el nombre del archivo generado (composite.jpg) o error si no hay
// frames decodificados para fusionar.
func (s *SessionStore) Composite() (string, error) {
	frames, err := s.loadDecodedFrames()
	if err != nil {
		return "", err
	}
	if len(frames) == 0 {
		return "", fmt.Errorf("composite: sin frames decodificados")
	}

	const outFile = "composite.jpg"
	if len(frames) == 1 {
		if err := s.encodeCompositeJPEG(outFile, frames[0]); err != nil {
			return "", err
		}
		s.setComposite(outFile, 1)
		return outFile, nil
	}

	// Normalizar dimensiones (todas deberían coincidir por el preset; si un
	// frame difiere se descarta en vez de abortar la fusión).
	w, h := frames[0].Bounds().Dx(), frames[0].Bounds().Dy()
	rgbas := make([]*image.RGBA, 0, len(frames))
	for _, f := range frames {
		if f.Bounds().Dx() != w || f.Bounds().Dy() != h {
			continue
		}
		rgbas = append(rgbas, toRGBA(f))
	}

	lumas := make([][]uint8, len(rgbas))
	for i, img := range rgbas {
		lumas[i] = lumaPlane(img)
	}

	// 1. Referencia: máxima energía de gradiente.
	ref := 0
	best := -1.0
	for i, l := range lumas {
		if e := gradientEnergy(l, w, h); e > best {
			best = e
			ref = i
		}
	}

	// 2. Traslación entera de cada frame hacia la referencia.
	const maxShift = 6
	shifts := make([][2]int, len(rgbas))
	for i := range rgbas {
		if i == ref {
			continue
		}
		shifts[i] = bestShift(lumas[ref], lumas[i], w, h, maxShift)
	}

	// 3. Mediana por píxel/canal sobre el stack alineado.
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	stackR := make([]uint8, 0, len(rgbas))
	stackG := make([]uint8, 0, len(rgbas))
	stackB := make([]uint8, 0, len(rgbas))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			stackR, stackG, stackB = stackR[:0], stackG[:0], stackB[:0]
			for i, img := range rgbas {
				sx, sy := x+shifts[i][0], y+shifts[i][1]
				if sx < 0 || sx >= w || sy < 0 || sy >= h {
					continue
				}
				o := img.PixOffset(sx, sy)
				stackR = append(stackR, img.Pix[o])
				stackG = append(stackG, img.Pix[o+1])
				stackB = append(stackB, img.Pix[o+2])
			}
			o := out.PixOffset(x, y)
			out.Pix[o] = medianU8(stackR)
			out.Pix[o+1] = medianU8(stackG)
			out.Pix[o+2] = medianU8(stackB)
			out.Pix[o+3] = 0xff
		}
	}

	if err := s.encodeCompositeJPEG(outFile, out); err != nil {
		return "", err
	}
	s.setComposite(outFile, len(rgbas))
	return outFile, nil
}

// CompositeFile devuelve el nombre del composite generado ("" si no hay) y
// cuántos frames lo componen.
func (s *SessionStore) CompositeFile() (string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.compositeFile, s.compositeFrames
}

func (s *SessionStore) setComposite(file string, n int) {
	s.mu.Lock()
	s.compositeFile = file
	s.compositeFrames = n
	s.mu.Unlock()
}

// loadDecodedFrames lee de disco los frames completos ya decodificados a JPEG
// (los .h264 sin ffmpeg no participan), ordenados por frameId.
func (s *SessionStore) loadDecodedFrames() ([]image.Image, error) {
	infos := s.List()
	sort.Slice(infos, func(i, j int) bool { return infos[i].FrameID < infos[j].FrameID })

	frames := make([]image.Image, 0, len(infos))
	for _, fi := range infos {
		if !strings.HasSuffix(fi.File, ".jpg") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, fi.File))
		if err != nil {
			continue
		}
		img, err := jpeg.Decode(bytes.NewReader(b))
		if err != nil {
			continue
		}
		frames = append(frames, img)
	}
	return frames, nil
}

func (s *SessionStore) encodeCompositeJPEG(file string, img image.Image) error {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return fmt.Errorf("composite: encode: %w", err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, file), buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("composite: write: %w", err)
	}
	return nil
}

func toRGBA(img image.Image) *image.RGBA {
	if rgba, ok := img.(*image.RGBA); ok {
		return rgba
	}
	out := image.NewRGBA(img.Bounds())
	draw.Draw(out, out.Bounds(), img, img.Bounds().Min, draw.Src)
	return out
}

func lumaPlane(img *image.RGBA) []uint8 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]uint8, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := img.PixOffset(x, y)
			out[y*w+x] = uint8((77*int(img.Pix[o]) + 150*int(img.Pix[o+1]) + 29*int(img.Pix[o+2])) >> 8)
		}
	}
	return out
}

// gradientEnergy mide nitidez: suma de gradientes al cuadrado (h+v).
func gradientEnergy(luma []uint8, w, h int) float64 {
	var sum float64
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			i := y*w + x
			dx := int(luma[i+1]) - int(luma[i-1])
			dy := int(luma[i+w]) - int(luma[i-w])
			sum += float64(dx*dx + dy*dy)
		}
	}
	return sum
}

// bestShift busca la traslación (dx,dy) que minimiza el SAD de luma entre la
// referencia y el frame, muestreando cada 2px para abaratar la búsqueda.
// A 320×240 con ±6: 169 offsets × ~19k muestras ≈ trivial en backend.
func bestShift(ref, frame []uint8, w, h, maxShift int) [2]int {
	bestSAD := int64(-1)
	var best [2]int
	for dy := -maxShift; dy <= maxShift; dy++ {
		for dx := -maxShift; dx <= maxShift; dx++ {
			var sad int64
			var n int64
			for y := maxShift; y < h-maxShift; y += 2 {
				for x := maxShift; x < w-maxShift; x += 2 {
					d := int(ref[y*w+x]) - int(frame[(y+dy)*w+(x+dx)])
					if d < 0 {
						d = -d
					}
					sad += int64(d)
					n++
				}
			}
			if n == 0 {
				continue
			}
			if bestSAD < 0 || sad < bestSAD {
				bestSAD = sad
				best = [2]int{dx, dy}
			}
		}
	}
	return best
}

func medianU8(v []uint8) uint8 {
	switch len(v) {
	case 0:
		return 0
	case 1:
		return v[0]
	}
	// Insertion sort: stacks de ≤45 elementos, más rápido que sort.Slice acá.
	for i := 1; i < len(v); i++ {
		x := v[i]
		j := i - 1
		for j >= 0 && v[j] > x {
			v[j+1] = v[j]
			j--
		}
		v[j+1] = x
	}
	return v[len(v)/2]
}
