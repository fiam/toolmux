package client

import (
	"testing"

	"github.com/fiam/toolmux/internal/providers/google/googleapi"
)

func imageTestDocument() googleapi.Document {
	return googleapi.Document{
		Body: googleapi.DocumentBody{
			Content: []googleapi.StructuralElement{{
				StartIndex: 1,
				EndIndex:   4,
				Paragraph: &googleapi.Paragraph{
					PositionedObjectIds: []string{"kix.pos1"},
					Elements: []googleapi.ParagraphElement{
						{StartIndex: 1, EndIndex: 2, TextRun: &googleapi.TextRun{Content: "A"}},
						{StartIndex: 2, EndIndex: 3, InlineObjectElement: &googleapi.InlineObjectElement{InlineObjectID: "kix.inline1"}},
					},
				},
			}},
		},
		InlineObjects: map[string]googleapi.InlineObject{
			"kix.inline1": {
				ObjectID: "kix.inline1",
				InlineObjectProperties: googleapi.InlineObjectProperties{
					EmbeddedObject: googleapi.EmbeddedObject{
						Title:           "Inline diagram",
						ImageProperties: &googleapi.ImageProperties{ContentURI: "https://example.com/c/inline1", SourceURI: "https://example.com/s/inline1"},
						Size: &googleapi.Size{
							Width:  googleapi.Dimension{Magnitude: 120, Unit: "PT"},
							Height: googleapi.Dimension{Magnitude: 80, Unit: "PT"},
						},
					},
				},
			},
		},
		PositionedObjects: map[string]googleapi.PositionedObject{
			"kix.pos1": {
				ObjectID: "kix.pos1",
				PositionedObjectProperties: googleapi.PositionedObjectProperties{
					EmbeddedObject: googleapi.EmbeddedObject{
						Title:           "Floating logo",
						ImageProperties: &googleapi.ImageProperties{ContentURI: "https://example.com/c/pos1"},
					},
				},
			},
		},
	}
}

func TestDocumentImagesSurfacesObjectsAndRanges(t *testing.T) {
	t.Parallel()
	images := documentImages(imageTestDocument())
	if len(images) != 2 {
		t.Fatalf("expected 2 images, got %d: %#v", len(images), images)
	}

	var inline, positioned *docsStructureMatch
	for i := range images {
		switch images[i].Kind {
		case "inline_image":
			inline = &images[i]
		case "positioned_image":
			positioned = &images[i]
		}
	}
	if inline == nil || positioned == nil {
		t.Fatalf("expected one inline and one positioned image, got %#v", images)
	}
	if inline.ObjectID != "kix.inline1" || inline.StartIndex != 2 || inline.EndIndex != 3 {
		t.Fatalf("unexpected inline image range/id: %#v", inline)
	}
	if inline.ContentURI != "https://example.com/c/inline1" || inline.SourceURI != "https://example.com/s/inline1" {
		t.Fatalf("unexpected inline image URIs: %#v", inline)
	}
	if inline.WidthPt != 120 || inline.HeightPt != 80 {
		t.Fatalf("unexpected inline image size: %#v", inline)
	}
	if positioned.ObjectID != "kix.pos1" || positioned.Title != "Floating logo" {
		t.Fatalf("unexpected positioned image: %#v", positioned)
	}
}

func TestFilterImageMatches(t *testing.T) {
	t.Parallel()
	images := documentImages(imageTestDocument())

	if got := filterImageMatches(images, "", false); len(got) != 2 {
		t.Fatalf("empty query should keep all images, got %d", len(got))
	}
	byID := filterImageMatches(images, "pos1", false)
	if len(byID) != 1 || byID[0].ObjectID != "kix.pos1" {
		t.Fatalf("expected object-id match, got %#v", byID)
	}
	byTitle := filterImageMatches(images, "inline diagram", false)
	if len(byTitle) != 1 || byTitle[0].ObjectID != "kix.inline1" {
		t.Fatalf("expected title match, got %#v", byTitle)
	}
	if got := filterImageMatches(images, "nonexistent", false); len(got) != 0 {
		t.Fatalf("expected no matches, got %#v", got)
	}
}

func TestValidatePublicImageURI(t *testing.T) {
	t.Parallel()
	if got, err := validatePublicImageURI("  https://cdn.example/x.png\n"); err != nil || got != "https://cdn.example/x.png" {
		t.Fatalf("expected trimmed valid URL, got %q err %v", got, err)
	}
	for _, bad := range []string{"", "   ", "ftp://example/x.png", "not a url", "/local/path.png"} {
		if _, err := validatePublicImageURI(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
	long := "https://cdn.example/" + string(make([]byte, 2048))
	if _, err := validatePublicImageURI(long); err == nil {
		t.Fatal("expected error for over-long URL")
	}
}

func TestImageExtension(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"image/png":  ".png",
		"image/jpeg": ".jpg",
		"image/jpg":  ".jpg",
		"image/gif":  ".gif",
		"image/webp": "",
		"":           "",
	}
	for mimeType, want := range cases {
		if got := imageExtension(mimeType); got != want {
			t.Errorf("imageExtension(%q) = %q, want %q", mimeType, got, want)
		}
	}
}
