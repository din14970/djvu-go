// Command test_read demonstrates the djvu-go library on a document given
// on the command line. It prints everything the library can extract:
// the document kind, page count and geometry, the INFO fields, hidden
// text and annotations of the first page (or of the page selected with
// the optional second argument), document metadata, the outline, the
// component files of multipage documents, and the low-level IFF chunk
// structure.
//
// Usage:
//
//	go run ./examples/test_read.go document.djvu [page]
//
// The optional page argument selects a page to dump in detail instead of
// the first page (zero-based).
package main

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"

	"codeberg.org/din14970/djvu-go"
	"codeberg.org/din14970/djvu-go/iff"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: test_read <file.djvu> [page]")
		os.Exit(2)
	}
	page := 0
	if len(os.Args) > 2 {
		p, err := strconv.Atoi(os.Args[2])
		if err != nil || p < 0 {
			fmt.Fprintln(os.Stderr, "test_read: page must be a non-negative number")
			os.Exit(2)
		}
		page = p
	}

	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "test_read:", err)
		os.Exit(1)
	}

	doc, err := djvu.Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "test_read:", err)
		os.Exit(1)
	}

	n, _ := doc.PageCount()
	fmt.Printf("kind:   %s\n", doc.Kind())
	fmt.Printf("pages:  %d\n\n", n)

	pages, err := doc.Pages()
	if err != nil {
		fmt.Println("Pages error:", err)
		os.Exit(1)
	}

	// Detail view of the selected page.
	if page >= len(pages) {
		fmt.Fprintf(os.Stderr, "test_read: page %d out of range (document has %d pages)\n", page, len(pages))
		os.Exit(1)
	}
	dumpPage(doc, pages[page])

	// Aggregate statistics over all pages.
	geometry := map[string]int{}
	textPages, textChars := 0, 0
	for _, p := range pages {
		geometry[fmt.Sprintf("%dx%d @ %d dpi (orientation %d)", p.Width, p.Height, p.DPI, p.Orientation)]++
		if t, err := doc.PageText(p.Index); err == nil && t != "" {
			textPages++
			textChars += len(t)
		}
	}
	fmt.Println("page geometries across the document:")
	for g, c := range geometry {
		fmt.Printf("  %d x %s\n", c, g)
	}
	fmt.Printf("pages with hidden text: %d/%d (%d characters total)\n\n", textPages, n, textChars)

	// Document metadata.
	meta, err := doc.Metadata()
	if err != nil {
		fmt.Println("Metadata error:", err)
	} else {
		fmt.Println("metadata:")
		fmt.Printf("  title:    %q\n", meta.Title)
		fmt.Printf("  author:   %q\n", meta.Author)
		fmt.Printf("  subject:  %q\n", meta.Subject)
		fmt.Printf("  keywords: %q\n", meta.Keywords)
		fmt.Printf("  producer: %q\n", meta.Producer)
		for k, v := range meta.Others {
			fmt.Printf("  %s: %q\n", k, v)
		}
		if meta.Title == "" && meta.Author == "" && meta.Producer == "" && len(meta.Others) == 0 {
			fmt.Println("  (no metadata stored in the document)")
		}
	}
	fmt.Println()

	// Outline.
	ol, err := doc.Outline()
	switch {
	case err != nil:
		fmt.Println("outline error:", err)
	case ol == nil:
		fmt.Println("outline: (none)")
	default:
		fmt.Println("outline:")
		var dump func(bs []djvu.Bookmark, ind string)
		dump = func(bs []djvu.Bookmark, ind string) {
			for _, b := range bs {
				fmt.Printf("%s%q -> %s\n", ind, b.Title, b.URL)
				dump(b.Children, ind+"    ")
			}
		}
		dump(ol, "  ")
	}
	fmt.Println()

	// Component files of multipage documents.
	if comps := doc.Components(); comps != nil {
		types := map[string]int{}
		for _, c := range comps {
			types[c.Type.String()]++
		}
		fmt.Printf("components: %d, types: %v\n", len(comps), types)
		for _, c := range comps {
			if c.Type != djvu.ComponentPage {
				fmt.Printf("  %q type=%s offset=%d size=%d\n", c.ID, c.Type, c.Offset, c.Size)
			}
		}
		fmt.Println()
	} else {
		fmt.Println("components: (single-page document)")
		fmt.Println()
	}

	// Low-level IFF chunk structure: the top level and, one level down,
	// the chunks of the selected page.
	fmt.Println("chunk structure:")
	chunks, err := iff.ParseChunks(bytes.NewReader(data))
	if err != nil {
		fmt.Println("iff parse error:", err)
		os.Exit(1)
	}
	for _, c := range chunks {
		fmt.Printf("  %s [%d] at %d\n", c.FullID(), c.Length, c.Start)
		children, err := c.Children()
		if err != nil {
			fmt.Printf("    (children: %v)\n", err)
			continue
		}
		for _, ch := range children {
			fmt.Printf("    %s [%d] at %d\n", ch.FullID(), ch.Length, ch.Start)
			if ch.Composite() && ch.Start == pages[page].Offset {
				inner, err := ch.Children()
				if err != nil {
					continue
				}
				for _, ic := range inner {
					fmt.Printf("      %s [%d] at %d\n", ic.FullID(), ic.Length, ic.Start)
				}
			}
		}
	}
}

// dumpPage prints the INFO fields, hidden text and annotations of one page.
func dumpPage(doc *djvu.Document, p djvu.PageInfo) {
	fmt.Printf("page %d (%q): %dx%d px, %d dpi, gamma %.1f, DjVu v%d, orientation %d, offset %d, size %d, hasInfo=%v\n",
		p.Index, p.Name, p.Width, p.Height, p.DPI, p.Gamma, p.Version, p.Orientation, p.Offset, p.Size, p.HasInfo)

	text, err := doc.PageText(p.Index)
	switch {
	case err != nil:
		fmt.Printf("  text:   error: %v\n", err)
	case text == "":
		fmt.Println("  text:   (none)")
	default:
		fmt.Println("  text:")
		for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
			fmt.Printf("    | %s\n", line)
		}
	}

	annos, err := doc.Annotations(p.Index)
	switch {
	case err != nil:
		fmt.Printf("  annos:  error: %v\n", err)
	case len(annos) == 0:
		fmt.Println("  annos:  (none)")
	default:
		fmt.Println("  annos:")
		for _, a := range annos {
			fmt.Printf("    %s\n", a)
		}
	}
	fmt.Println()
}
