// Command webserver serves the pages of a DjVu document as PNG images
// over HTTP. It is the headline example of the library: open a
// document, then render pages on demand.
//
// Usage:
//
//	go run ./examples/webserver file.djvu [addr]
//
// The default address is localhost:8080. Pages are served under
// /page/N.png at full resolution and /page/N/sub/S.png at a reduced
// resolution (S is the subsampling factor: 2 renders at half
// resolution, 4 at a quarter, and so on). Decoded pages are cached, so
// repeated requests do not decode again.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"

	"codeberg.org/din14970/djvu-go"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: webserver file.djvu [addr]")
		os.Exit(2)
	}
	addr := "localhost:8080"
	if len(os.Args) > 2 {
		addr = os.Args[2]
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		_ = f.Close()
	}()
	fi, err := f.Stat()
	if err != nil {
		log.Fatal(err)
	}
	doc, err := djvu.Open(f, fi.Size())
	if err != nil {
		log.Fatal(err)
	}
	n, err := doc.PageCount()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s: %d page(s); serving PNGs on http://%s/page/N.png\n", os.Args[1], n, addr)

	full := regexp.MustCompile(`^/page/([0-9]+)\.png$`)
	sub := regexp.MustCompile(`^/page/([0-9]+)/sub/([0-9]+)\.png$`)
	text := regexp.MustCompile(`^/page/([0-9]+)/text\.txt$`)
	render := func(w http.ResponseWriter, page, subsample int) {
		if page < 0 || page >= n {
			http.NotFound(w, nil)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		if err := doc.RenderPNG(w, page, djvu.RenderOptions{Subsample: subsample}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if m := full.FindStringSubmatch(r.URL.Path); m != nil {
			page, _ := strconv.Atoi(m[1])
			render(w, page, 1)
			return
		}
		if m := sub.FindStringSubmatch(r.URL.Path); m != nil {
			page, _ := strconv.Atoi(m[1])
			sub, _ := strconv.Atoi(m[2])
			render(w, page, sub)
			return
		}
		if m := text.FindStringSubmatch(r.URL.Path); m != nil {
			page, _ := strconv.Atoi(m[1])
			if page < 0 || page >= n {
				http.NotFound(w, nil)
				return
			}
			t, err := doc.PageText(page)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			if _, err := w.Write([]byte(t)); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		http.NotFound(w, nil)
	})
	log.Fatal(http.ListenAndServe(addr, nil))
}
