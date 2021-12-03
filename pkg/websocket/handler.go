package websocket

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/adjust/rmq/v4"
	"github.com/go-playground/validator"
	"github.com/ilhasoft/wwcs/pkg/queue"
	log "github.com/sirupsen/logrus"
)

// SetupRoutes handle all routes
func SetupRoutes(app *App) {
	log.Trace("Setting up routes")

	http.HandleFunc("/ws", app.WSHandler)
	http.HandleFunc("/send", app.SendHandler)
	http.HandleFunc("/healthcheck", app.HealthCheckHandler)
}

func (a *App) WSHandler(w http.ResponseWriter, r *http.Request) {
	log.Trace("Serving websocket")
	conn, err := Upgrade(w, r)
	if err != nil {
		log.Error(err)
		fmt.Fprint(w, "%+V\n", err)
	}

	client := &Client{
		Conn: conn,
	}

	client.Read(a)
}

var validate = validator.New()

var (
	ErrorConnectionClosed = errors.New("unable to send: connection closed")
	ErrorInternalError    = errors.New("unable to send: internal error")
	ErrorBadRequest       = errors.New("unable to send: bad request")
	ErrorNotFound         = errors.New("unable to send: not found")
	ErrorAWSConnection    = errors.New("unable to connect to AWS")
)

// SendHandler is used to receive messages from external systems
func (a *App) SendHandler(w http.ResponseWriter, r *http.Request) {
	log.Tracef("Receiving message from %q", r.Host)
	payload := IncomingPayload{}
	err := json.NewDecoder(r.Body).Decode(&payload)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(ErrorBadRequest.Error()))
		return
	}

	err = validate.Struct(payload)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(ErrorBadRequest.Error()))
		return
	}

	c, found := a.Pool.Clients[payload.To]
	if !found {
		payloadMarshalled, err := json.Marshal(payload)
		if err != nil {
			log.Error("error to parse incoming payload: ", err)
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(ErrorInternalError.Error()))
			return
		}
		queueConnection, err := rmq.OpenConnectionWithRedisClient("wwcs-service", a.RDB, nil)
		if err != nil {
			log.Error("error to open redis message queue connection: ", err)
		}
		defer queueConnection.StopHeartbeat()
		cQueue := queue.OpenQueue(payload.To, queueConnection)
		err = cQueue.Publish(string(payloadMarshalled))
		if err != nil {
			log.Error("error to publish incoming payload: ", err)
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(ErrorInternalError.Error()))
			return
		}
	} else {
		err = c.Send(payload)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(ErrorInternalError.Error()))
			return
		}
	}
	w.WriteHeader(http.StatusAccepted)
}

// HealthCheckHandler is used to provide a mechanism to check the service status
func (a *App) HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	err := CheckAWS()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(ErrorAWSConnection.Error()))
		return
	}

	w.WriteHeader(http.StatusOK)
}
