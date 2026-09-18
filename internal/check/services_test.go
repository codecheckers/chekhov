package check

import (
	"net/http"
	"reflect"
	"testing"
)

// Fresh copies the configured fields by hand, and a field added to Services
// and forgotten there is empty for every command a deployment runs - which is
// exactly how the OpenAlex base URL went missing. Reflection, so that the next
// field cannot be forgotten quietly.
func TestFreshCarriesEveryConfiguredField(t *testing.T) {
	services := &Services{HTTP: http.DefaultClient}
	value := reflect.ValueOf(services).Elem()
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		if field.Kind() == reflect.String && field.CanSet() {
			field.SetString("set-" + value.Type().Field(i).Name)
		}
	}

	fresh := reflect.ValueOf(services.Fresh()).Elem()
	for i := 0; i < value.NumField(); i++ {
		name := value.Type().Field(i).Name
		if value.Field(i).Kind() != reflect.String || !value.Field(i).CanSet() {
			continue
		}
		if got := fresh.Field(i).String(); got != "set-"+name {
			t.Errorf("Fresh() dropped %s: %q", name, got)
		}
	}
	if fresh.FieldByName("HTTP").IsNil() {
		t.Error("Fresh() dropped the HTTP client")
	}
}
