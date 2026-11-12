package csvutil

import (
	"strings"
	"testing"
)

func TestParseWithHeader(t *testing.T) {
	res := Parse(strings.NewReader("Имя,Email\nИван,Ivan@Example.COM\nМария,maria@mail.ru\nБитая строка без email\nИван второй,ivan@example.com\n"))
	if len(res.Emails) != 2 {
		t.Fatalf("emails = %v", res.Emails)
	}
	if res.Emails[0] != "ivan@example.com" || res.Names[0] != "Иван" {
		t.Errorf("first row: %q %q", res.Emails[0], res.Names[0])
	}
	if res.Emails[1] != "maria@mail.ru" || res.Names[1] != "Мария" {
		t.Errorf("second row: %q %q", res.Emails[1], res.Names[1])
	}
	if res.Duplicates != 1 {
		t.Errorf("duplicates = %d", res.Duplicates)
	}
	if len(res.Skipped) != 1 {
		t.Errorf("skipped = %v", res.Skipped)
	}
}

func TestParseNoHeader(t *testing.T) {
	res := Parse(strings.NewReader("a@one.ru,Аня\nb@two.ru\n"))
	if len(res.Emails) != 2 || res.Emails[0] != "a@one.ru" || res.Names[0] != "Аня" || res.Names[1] != "" {
		t.Errorf("emails=%v names=%v", res.Emails, res.Names)
	}
}

func TestParseSemicolonAndEmpty(t *testing.T) {
	res := Parse(strings.NewReader("email;name\nx@x.io;Вася\n"))
	if len(res.Emails) != 1 || res.Names[0] != "Вася" {
		t.Errorf("emails=%v names=%v", res.Emails, res.Names)
	}
	if got := Parse(strings.NewReader("")); len(got.Emails) != 0 {
		t.Error("empty input should produce nothing")
	}
}
