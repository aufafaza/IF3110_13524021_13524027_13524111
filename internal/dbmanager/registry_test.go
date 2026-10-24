package dbmanager

import (
	"errors"
	"testing"
)

func TestValidate(t *testing.T) {
	ok := DatabaseInput{Name: "n", Host: "h", DBName: "d", DBUser: "u", Password: "p"}

	in := ok
	if err := in.validate(true); err != nil {
		t.Fatal(err)
	}
	if in.Port != 5432 || in.SSLMode != "prefer" {
		t.Errorf("default salah: port=%d sslmode=%q", in.Port, in.SSLMode)
	}

	blankPw := ok
	blankPw.Password = ""
	if err := blankPw.validate(false); err != nil {
		t.Errorf("password boleh kosong saat update: %v", err)
	}

	bad := []struct {
		name      string
		mutate    func(*DatabaseInput)
		requirePw bool
	}{
		{"name kosong", func(i *DatabaseInput) { i.Name = "" }, true},
		{"host kosong", func(i *DatabaseInput) { i.Host = "" }, true},
		{"dbname kosong", func(i *DatabaseInput) { i.DBName = "" }, true},
		{"user kosong", func(i *DatabaseInput) { i.DBUser = "" }, true},
		{"port negatif", func(i *DatabaseInput) { i.Port = -1 }, true},
		{"port terlalu besar", func(i *DatabaseInput) { i.Port = 70000 }, true},
		{"password wajib saat create", func(i *DatabaseInput) { i.Password = "" }, true},
		{"sslmode tidak dikenal", func(i *DatabaseInput) { i.SSLMode = "bogus" }, true},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			in := ok
			tt.mutate(&in)
			if err := in.validate(tt.requirePw); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}