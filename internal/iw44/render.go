package iw44

import "image"

// Gray renders the image as a grayscale image at the given subsampling
// factor (1, 2, 4, 8 or 16). It returns nil if no chunk has been
// decoded. For color images the luma component is rendered.
func (im *Image) Gray(subsample int) *image.Gray {
	if im.w == 0 {
		return nil
	}
	y := im.component(subsample, 0)
	w := (im.w + subsample - 1) / subsample
	h := (im.h + subsample - 1) / subsample
	out := image.NewGray(image.Rect(0, 0, w, h))
	for i := 0; i < h; i++ {
		// DjVu coordinates grow from the bottom up; flip rows so the
		// output matches the top down convention of the image package.
		src := (h - 1 - i) * w
		for j := 0; j < w; j++ {
			// The reconstruction yields signed values; gray level is
			// the value plus 128.
			out.Pix[i*out.Stride+j] = uint8(int(y[src+j]) + 128)
		}
	}
	return out
}

// RGBA renders the image as a color image at the given subsampling
// factor. Gray images are rendered as gray RGB. Color images combine
// the luma and chroma components with the Pigeon YCbCr to RGB
// conversion of the reference implementation.
func (im *Image) RGBA(subsample int) *image.RGBA {
	if im.w == 0 {
		return nil
	}
	w := (im.w + subsample - 1) / subsample
	h := (im.h + subsample - 1) / subsample
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	y := im.component(subsample, 0)
	if im.cbb == nil || im.crb == nil || im.crcbDelay < 0 {
		// Gray data. Inside a color container (pixmap), the reference
		// renders gray with inverted polarity (IWPixmap's fallback);
		// standalone gray images use the plain gray level.
		for i := 0; i < h; i++ {
			// Flip rows: DjVu coordinates grow from the bottom up.
			src := (h - 1 - i) * w
			for j := 0; j < w; j++ {
				var g uint8
				if im.pixmap {
					g = uint8(127 - int(y[src+j]))
				} else {
					g = uint8(int(y[src+j]) + 128)
				}
				o := i*out.Stride + j*4
				out.Pix[o] = g
				out.Pix[o+1] = g
				out.Pix[o+2] = g
				out.Pix[o+3] = 0xff
			}
		}
		return out
	}
	cb := im.cbb.component(subsample, im.crcbHalf)
	cr := im.crb.component(subsample, im.crcbHalf)
	for i := 0; i < h; i++ {
		// Flip rows: DjVu coordinates grow from the bottom up.
		src := (h - 1 - i) * w
		for j := 0; j < w; j++ {
			// Pigeon transform (Transform::Decode::YCbCr_to_RGB).
			yc := int(y[src+j])
			b := int(cb[src+j])
			r := int(cr[src+j])
			t1 := b >> 2
			t2 := r + (r >> 1)
			t3 := yc + 128 - t1
			tr := yc + 128 + t2
			tg := t3 - (t2 >> 1)
			tb := t3 + (b << 1)
			clamp := func(x int) uint8 {
				if x < 0 {
					return 0
				} else if x > 255 {
					return 255
				}
				return uint8(x)
			}
			o := i*out.Stride + j*4
			out.Pix[o] = clamp(tr)
			out.Pix[o+1] = clamp(tg)
			out.Pix[o+2] = clamp(tb)
			out.Pix[o+3] = 0xff
		}
	}
	return out
}
