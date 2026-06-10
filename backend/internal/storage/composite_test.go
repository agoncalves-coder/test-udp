package storage

import (
	"bytes"
	"image"
	"image/jpeg"
	"math/rand"
	"os"
	"testing"
	"time"

	"face-capture-poc/backend/internal/experiments"
	"face-capture-poc/backend/internal/protocol"
	"face-capture-poc/backend/internal/reassembler"
)

const compW, compH = 160, 120

// cleanBase: imagen sintética con estructura (gradientes + bloques) para que
// la alineación tenga señal con la que trabajar.
func cleanBase() *image.Gray {
	img := image.NewGray(image.Rect(0, 0, compW, compH))
	for y := 0; y < compH; y++ {
		for x := 0; x < compW; x++ {
			v := (x*2 + y) % 256
			if (x/20+y/20)%2 == 0 {
				v = 255 - v
			}
			img.Pix[y*compW+x] = uint8(v)
		}
	}
	return img
}

// noisyShifted: copia de base desplazada (dx,dy) con ruido sal-y-pimienta.
func noisyShifted(base *image.Gray, dx, dy int, noisePct float64, rng *rand.Rand) *image.Gray {
	out := image.NewGray(base.Bounds())
	for y := 0; y < compH; y++ {
		for x := 0; x < compW; x++ {
			sx, sy := x+dx, y+dy
			v := uint8(128)
			if sx >= 0 && sx < compW && sy >= 0 && sy < compH {
				v = base.Pix[sy*compW+sx]
			}
			if rng.Float64() < noisePct {
				if rng.Intn(2) == 0 {
					v = 0
				} else {
					v = 255
				}
			}
			out.Pix[y*compW+x] = v
		}
	}
	return out
}

// minShiftMAE: MAE mínimo entre a y b probando corrimientos globales ±r,
// evaluado sobre la región interior común (excluye bordes sin solape).
func minShiftMAE(a, b []uint8, w, h, r int) float64 {
	best := -1.0
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			var sum float64
			var n int
			for y := r; y < h-r; y++ {
				for x := r; x < w-r; x++ {
					d := int(a[y*w+x]) - int(b[(y+dy)*w+(x+dx)])
					if d < 0 {
						d = -d
					}
					sum += float64(d)
					n++
				}
			}
			if mae := sum / float64(n); best < 0 || mae < best {
				best = mae
			}
		}
	}
	return best
}

func meanAbsErr(a, b []uint8) float64 {
	var sum float64
	for i := range a {
		d := int(a[i]) - int(b[i])
		if d < 0 {
			d = -d
		}
		sum += float64(d)
	}
	return sum / float64(len(a))
}

func ingestJPEG(t *testing.T, s *SessionStore, frameID uint16, img image.Image) {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 92}); err != nil {
		t.Fatal(err)
	}
	err := s.OnFrameComplete(reassembler.CompleteFrame{
		FrameID: frameID, Codec: protocol.CodecJPEG, Data: buf.Bytes(),
		FirstChunkAt: time.Now(), CompletedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCompositeReducesNoiseAndAligns(t *testing.T) {
	preset, _ := experiments.ByID("E1-baseline-3g") // 160x120
	s := testStore(t, preset, "")

	base := cleanBase()
	rng := rand.New(rand.NewSource(42))

	// 9 frames: ruido 4% y desplazamientos de hasta ±4 px (mano temblorosa).
	shifts := [][2]int{{0, 0}, {1, 0}, {-1, 1}, {2, -1}, {0, 2}, {-2, -2}, {3, 1}, {-1, -3}, {4, 0}}
	var firstNoisy *image.Gray
	for i, sh := range shifts {
		f := noisyShifted(base, sh[0], sh[1], 0.04, rng)
		if i == 0 {
			firstNoisy = f
		}
		ingestJPEG(t, s, uint16(i), f)
	}

	file, err := s.Composite()
	if err != nil {
		t.Fatal(err)
	}
	if file != "composite.jpg" {
		t.Errorf("file = %q", file)
	}
	name, n := s.CompositeFile()
	if name != "composite.jpg" || n != 9 {
		t.Errorf("CompositeFile() = %q, %d; esperaba composite.jpg, 9", name, n)
	}

	// Decodificar el composite y compararlo contra la base limpia.
	raw, err := readFileInStore(s, file)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("composite no decodifica: %v", err)
	}
	if b := img.Bounds(); b.Dx() != compW || b.Dy() != compH {
		t.Fatalf("bounds = %v", b)
	}

	compLuma := grayPix(img)
	baseLuma := base.Pix
	noisyLuma := firstNoisy.Pix

	// El composite queda registrado en la grilla del frame de REFERENCIA
	// (el más nítido), que puede estar desplazado respecto de la base. Para
	// medir solo ruido, comparamos con invariancia al corrimiento global.
	maeComposite := minShiftMAE(compLuma, baseLuma, compW, compH, 5)
	maeSingle := meanAbsErr(noisyLuma, baseLuma) // frame 0 tiene shift {0,0}

	// La fusión debe acercarse a la imagen limpia bastante más que cualquier
	// frame individual ruidoso (acá: al menos 2x mejor).
	if maeComposite >= maeSingle/2 {
		t.Errorf("MAE composite = %.2f, frame ruidoso = %.2f: la fusión no mejora lo suficiente",
			maeComposite, maeSingle)
	}
	t.Logf("MAE composite %.2f vs frame individual %.2f", maeComposite, maeSingle)
}

func TestCompositeSingleFrame(t *testing.T) {
	preset, _ := experiments.ByID("E1-baseline-3g")
	s := testStore(t, preset, "")
	ingestJPEG(t, s, 0, cleanBase())

	file, err := s.Composite()
	if err != nil {
		t.Fatal(err)
	}
	if _, n := s.CompositeFile(); n != 1 {
		t.Errorf("compositeFrames = %d, esperaba 1", n)
	}
	raw, err := readFileInStore(s, file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(raw)); err != nil {
		t.Errorf("composite de 1 frame no decodifica: %v", err)
	}
}

func TestCompositeNoFrames(t *testing.T) {
	preset, _ := experiments.ByID("E1-baseline-3g")
	s := testStore(t, preset, "")
	if _, err := s.Composite(); err == nil {
		t.Error("sin frames debe devolver error")
	}
}

func grayPix(img image.Image) []uint8 {
	b := img.Bounds()
	out := make([]uint8, b.Dx()*b.Dy())
	i := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bb, _ := img.At(x, y).RGBA()
			out[i] = uint8((77*int(r>>8) + 150*int(g>>8) + 29*int(bb>>8)) >> 8)
			i++
		}
	}
	return out
}

func readFileInStore(s *SessionStore, file string) ([]byte, error) {
	return os.ReadFile(s.FramePath(file))
}
