package wanxiao

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBalanceParsingRejectsInvalidReadings(t *testing.T) {
	for _, balance := range []string{"23.45", "-2.5", "0", "", "bad", "NaN", "+Inf"} {
		t.Run(balance, func(t *testing.T) {
			inner, _ := json.Marshal(Body{RoomFull: "测试宿舍", RoomVerify: "room-a", DetailList: []Detail{{Odd: balance, BusinessType: "electric"}}})
			outer, _ := json.Marshal(Response{Code: 0, Body: string(inner)})
			client := Client{HTTPClient: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(outer)))}, nil
			})}}
			rooms, err := client.GetBalance("student", "1399")
			valid := balance == "23.45" || balance == "-2.5" || balance == "0"
			if valid {
				if err != nil || len(rooms) != 1 || rooms[0].MeterType != "electric" {
					t.Fatal(rooms, err)
				}
			} else if err == nil {
				t.Fatal("invalid balance became zero", rooms)
			}
		})
	}
}
