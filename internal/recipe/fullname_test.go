package recipe

import "testing"

func TestValidateFullname(t *testing.T) {
	tests := []struct {
		name     string
		fullname string
		wantErr  bool
	}{
		{name: "empty"},
		{name: "normal", fullname: "Ada Lovelace"},
		{name: "unicode", fullname: "Zoë 雪"},
		{name: "colon", fullname: "Bad:Name", wantErr: true},
		{name: "newline", fullname: "Bad\nName", wantErr: true},
		{name: "carriage return", fullname: "Bad\rName", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFullname(tt.fullname)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateFullname(%q) error = %v, wantErr %v", tt.fullname, err, tt.wantErr)
			}
		})
	}
}
