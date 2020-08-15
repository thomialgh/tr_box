package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

type messageType int

type redisPool struct {
	Pool *redis.Pool
}

type responseFormat struct {
	StatusCode int         `json:"status"`
	Message    string      `json:"msg,omitempty"`
	Data       interface{} `json:"data,omitempty"`
}

type clientInfo struct {
	Conn       *websocket.Conn
	ClientName string
	Chatbox    string
}

type messageFormat struct {
	Username string `json:"username"`
	Message  string `json:"message"`
}

type conn struct {
	Conn redis.Conn
}

var (
	pool     redisPool
	upgrader = websocket.Upgrader{
		ReadBufferSize:    1024,
		WriteBufferSize:   1024,
		EnableCompression: false,
	}
)

func main() {
	pool.Pool = redis.NewPool(func() (redis.Conn, error) {
		return redis.Dial("tcp", ":6379")
	}, 10)

	defer pool.Pool.Close()

	r := echo.New()
	r.Use(middleware.Logger())
	r.GET("/ws", connectClient)
	r.GET("", func(c echo.Context) error {
		return c.File("home.html")
	})
	server := &http.Server{
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		Addr:         ":19123",
	}

	panic(server.ListenAndServe())
}

func connectClient(c echo.Context) error {
	username := c.QueryParam("name")
	chatbox := c.QueryParam("chatbox")
	if username == "" || chatbox == "" {
		return c.JSON(400, responseFormat{
			StatusCode: 400,
			Message:    "query name or chatbox must be filled",
		})
	}
	conn, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		fmt.Println(err)
		return c.JSON(500, responseFormat{
			StatusCode: 500,
			Message:    "Failed to create connection",
		})
	}
	client := &clientInfo{
		Conn:       conn,
		ClientName: username,
		Chatbox:    chatbox,
	}

	go client.Run()
	return c.JSON(200, responseFormat{
		StatusCode: 200,
		Message:    "OK",
	})
}

func (c clientInfo) Run() {

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		fmt.Println("selesai")
		cancel()
		c.Conn.Close()
	}()
	conn := pool.getConn()
	defer conn.Conn.Close()
	msgChan := make(chan []byte)
	stopChan := make(chan bool)
	go conn.Subscribe(ctx, msgChan, c.Chatbox)
	go func() {
		defer func() {
			stopChan <- true
		}()
		conn := pool.getConn()
		defer conn.Conn.Close()
		for {
			_, msg, err := c.Conn.ReadMessage()
			if err != nil {
				log.Println(err)
				return
			}
			msg, err = marshalJSON(messageFormat{
				Username: c.ClientName,
				Message:  string(msg),
			})
			if err != nil {
				return
			}
			if i, err := conn.Publish(c.Chatbox, string(msg)); err != nil {
				return
			} else if i != 1 {
				fmt.Println(i)
				continue
			}
		}
	}()

	for {
		select {
		case msg := <-msgChan:
			c.Conn.WriteJSON(string(msg))
		case <-stopChan:
			return
		}
	}

}

func parseJSON(msg []byte) (res messageFormat, err error) {
	err = json.Unmarshal(msg, &res)
	return
}

func marshalJSON(data messageFormat) ([]byte, error) {
	return json.Marshal(data)
}

func (p redisPool) getConn() *conn {
	return &conn{
		Conn: p.Pool.Get(),
	}
}

func (c conn) Publish(key string, msg string) (interface{}, error) {
	return c.Conn.Do("PUBLISH", key, msg)
}

func (c conn) Subscribe(ctx context.Context, msg chan []byte, key string) {
	psc := redis.PubSubConn{Conn: c.Conn}
	defer func() {
		fmt.Println("selesai2")
		psc.Close()
	}()
	for {
		psc.Subscribe(key)
		select {
		case <-ctx.Done():
			return
		default:
			switch v := psc.Receive().(type) {
			case redis.Message:
				msg <- v.Data
			}
		}

	}

}
