package main

import "fmt"
import "net/http"

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		val := r.URL.String()
		fmt.Println(val)
		w.Write([]byte(val))
	})

	if err := http.ListenAndServe(":10000", nil); err != nil {
		panic(err)
	}
}
