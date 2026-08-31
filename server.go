package main 
import (
	"fmt"
	"net/http"
)

func startServer(){
	listener, err := net.Listen("tcp", ":6379")
	if err != nil {
		fmt.Println("Error starting server:", err)
		return
	}
	defer listener.Close()
}