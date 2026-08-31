package client

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSheetsGridRangeParsesSupportedA1Forms(t *testing.T) {
	t.Parallel()

	tests := map[string]map[string]any{
		"A1": {
			"sheetId": 7, "startRowIndex": 0, "endRowIndex": 1,
			"startColumnIndex": 0, "endColumnIndex": 1,
		},
		"B2:D5": {
			"sheetId": 7, "startRowIndex": 1, "endRowIndex": 5,
			"startColumnIndex": 1, "endColumnIndex": 4,
		},
		"A:D": {
			"sheetId": 7, "startColumnIndex": 0, "endColumnIndex": 4,
		},
		"2:5": {
			"sheetId": 7, "startRowIndex": 1, "endRowIndex": 5,
		},
		"$AA$10:$AB$12": {
			"sheetId": 7, "startRowIndex": 9, "endRowIndex": 12,
			"startColumnIndex": 26, "endColumnIndex": 28,
		},
	}
	for input, want := range tests {
		got, err := sheetsGridRange(7, input)
		if err != nil {
			t.Fatalf("sheetsGridRange(%q): %v", input, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("sheetsGridRange(%q) = %#v, want %#v", input, got, want)
		}
	}
}

func TestSheetsGridRangeRejectsUnsafeOrInvalidA1Forms(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"", "Sheet1!A1:B2", "A1:D", "D5:A1", "0", "A0", "A:B:C"} {
		if _, err := sheetsGridRange(0, input); err == nil {
			t.Fatalf("expected sheetsGridRange(%q) to fail", input)
		}
	}
}

func TestDecodeSheetsValuesJSONPreservesScalarTypes(t *testing.T) {
	t.Parallel()

	values, err := decodeSheetsValuesJSON(`[["formula",12,true,null],["=A1*2",3.5,false,""]]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || len(values[0]) != 4 {
		t.Fatalf("unexpected values: %#v", values)
	}
	if number, ok := values[0][1].(json.Number); !ok || number.String() != "12" {
		t.Fatalf("expected JSON number preservation, got %#v", values[0][1])
	}
	if values[0][2] != true || values[0][3] != nil || values[1][0] != "=A1*2" {
		t.Fatalf("unexpected scalar values: %#v", values)
	}
}

func TestDecodeSheetsValuesRejectsObjectsAndNonRows(t *testing.T) {
	t.Parallel()

	for _, input := range []string{`{"a":1}`, `[1,2]`, `[[{"a":1}]]`, `[]`, `[[1]] {}`} {
		if _, err := decodeSheetsValuesJSON(input); err == nil {
			t.Fatalf("expected decodeSheetsValuesJSON(%q) to fail", input)
		}
	}
}

func TestDecodeSheetsDelimitedValues(t *testing.T) {
	t.Parallel()

	csvValues, err := decodeSheetsDelimitedValues("Name,Count\nOpen,12\n", "csv")
	if err != nil {
		t.Fatal(err)
	}
	wantCSV := [][]any{{"Name", "Count"}, {"Open", "12"}}
	if !reflect.DeepEqual(csvValues, wantCSV) {
		t.Fatalf("unexpected CSV values: %#v", csvValues)
	}

	tsvValues, err := decodeSheetsDelimitedValues("Name\tCount\nOpen\t12\n", "tsv")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tsvValues, wantCSV) {
		t.Fatalf("unexpected TSV values: %#v", tsvValues)
	}
}

func TestSheetsEnumRejectsUnsupportedValues(t *testing.T) {
	t.Parallel()

	// Enum validation is also exercised through end-to-end command tests; keep
	// the parser cases close to the formatting request builder for clear errors.
	for _, test := range []struct {
		value   string
		allowed []string
	}{
		{value: "justify", allowed: []string{"LEFT", "CENTER", "RIGHT"}},
		{value: "accounting", allowed: []string{"TEXT", "NUMBER", "PERCENT"}},
	} {
		if _, err := sheetsEnum(test.value, "", test.allowed...); err == nil {
			t.Fatalf("expected sheetsEnum(%q) to fail", test.value)
		}
	}
}
