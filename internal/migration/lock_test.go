package migration

import "testing"

func TestAdvisoryLockKeyBounds(t *testing.T) {
	for _, test := range []struct {
		name       string
		databaseID int64
		want       int32
		wantErr    bool
	}{
		{name: "minimum", databaseID: minAdvisoryLockKey, want: -2147483648},
		{name: "maximum", databaseID: maxAdvisoryLockKey, want: 2147483647},
		{name: "below minimum", databaseID: minAdvisoryLockKey - 1, wantErr: true},
		{name: "above maximum", databaseID: maxAdvisoryLockKey + 1, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := advisoryLockKey(test.databaseID)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, test.wantErr)
			}
			if !test.wantErr && got != test.want {
				t.Fatalf("key = %d, want %d", got, test.want)
			}
		})
	}
}
