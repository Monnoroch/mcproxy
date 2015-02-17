package main

import "fmt"
import "net/http"

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Println(r.URL)
		w.Write([]byte(r.URL.String()))
	})

	if err := http.ListenAndServe(":10000", nil); err != nil {
		panic(err)
	}
}

