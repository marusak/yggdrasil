package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"git.sr.ht/~spc/go-log"
	"github.com/redhatinsights/yggdrasil"
	pb "github.com/redhatinsights/yggdrasil/protocol"
	"google.golang.org/grpc"
)

type worker struct {
	pid             int
	handler         string
	addr            string
	features        map[string]string
	detachedContent bool
}

type dispatcher struct {
	pb.UnimplementedDispatcherServer
	dispatchers chan map[string]map[string]string
	sendQ       chan yggdrasil.Data
	recvQ       chan yggdrasil.Data
	deadWorkers chan int
	reg         registry
	pidHandlers map[int]string
}

func newDispatcher() *dispatcher {
	return &dispatcher{
		dispatchers: make(chan map[string]map[string]string),
		sendQ:       make(chan yggdrasil.Data),
		recvQ:       make(chan yggdrasil.Data),
		deadWorkers: make(chan int),
		reg:         registry{},
		pidHandlers: make(map[int]string),
	}
}

func (d *dispatcher) Register(ctx context.Context, r *pb.RegistrationRequest) (*pb.RegistrationResponse, error) {
	if d.reg.get(r.GetHandler()) != nil {
		log.Errorf("worker failed to register for handler %v", r.GetHandler())
		return &pb.RegistrationResponse{Registered: false}, nil
	}

	w := worker{
		pid:             int(r.GetPid()),
		handler:         r.GetHandler(),
		addr:            fmt.Sprintf("@ygg-%v-%v", r.GetHandler(), randomString(6)),
		features:        r.GetFeatures(),
		detachedContent: r.GetDetachedContent(),
	}

	d.reg.set(r.GetHandler(), &w)
	d.pidHandlers[int(r.GetPid())] = r.GetHandler()

	log.Infof("worker registered: %+v", w)

	d.sendDispatchersMap()

	return &pb.RegistrationResponse{Registered: true, Address: w.addr}, nil
}

func (d *dispatcher) Send(ctx context.Context, r *pb.Data) (*pb.Receipt, error) {
	data := yggdrasil.Data{
		Type:       yggdrasil.MessageTypeData,
		MessageID:  r.GetMessageId(),
		ResponseTo: r.GetResponseTo(),
		Version:    1,
		Sent:       time.Now(),
		Directive:  r.GetDirective(),
		Metadata:   r.GetMetadata(),
		Content:    r.GetContent(),
	}

	URL, err := url.Parse(data.Directive)
	if err != nil {
		e := fmt.Errorf("cannot parse message content as URL: %w", err)
		log.Error(e)
		return nil, e
	}

	if URL.Scheme == "" {
		d.recvQ <- data
	} else {
		if yggdrasil.DataHost != "" {
			URL.Host = yggdrasil.DataHost
		}
		if err := post(URL.String(), data.Metadata, data.Content); err != nil {
			e := fmt.Errorf("cannot post detached message content: %w", err)
			log.Error(e)
			return nil, e
		}
	}
	log.Debugf("received message %v", data.MessageID)
	log.Tracef("message: %+v", data.Content)

	return &pb.Receipt{}, nil
}

// sendData receives values on a channel and sends the data over gRPC
func (d *dispatcher) sendData() {
	for data := range d.sendQ {
		f := func() {
			w := d.reg.get(data.Directive)

			if w == nil {
				log.Warnf("cannot route message to directive: %v", data.Directive)
				return
			}

			if w.detachedContent {
				var urlString string
				if err := json.Unmarshal(data.Content, &urlString); err != nil {
					log.Errorf("cannot unmarshal message content: %v", err)
					return
				}
				URL, err := url.Parse(urlString)
				if err != nil {
					log.Errorf("cannot parse message content as URL: %v", err)
					return
				}
				if yggdrasil.DataHost != "" {
					URL.Host = yggdrasil.DataHost
				}
				content, err := get(URL.String())
				if err != nil {
					log.Errorf("cannot get detached message content: %v", err)
					return
				}
				data.Content = content
			}

			conn, err := grpc.Dial("unix:"+w.addr, grpc.WithInsecure())
			if err != nil {
				log.Errorf("cannot dial socket: %v", err)
				return
			}
			defer conn.Close()

			c := pb.NewWorkerClient(conn)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			msg := pb.Data{
				MessageId:  data.MessageID,
				ResponseTo: data.ResponseTo,
				Directive:  data.Directive,
				Metadata:   data.Metadata,
				Content:    data.Content,
			}
			_, err = c.Send(ctx, &msg)
			if err != nil {
				log.Errorf("cannot send message %v: %v", data.MessageID, err)
				log.Tracef("message: %+v", data)
				return
			}
			log.Debugf("dispatched message %v to worker %v", msg.MessageId, data.Directive)
		}

		f()
	}
}

func (d *dispatcher) unregisterWorker() {
	for pid := range d.deadWorkers {
		handler := d.pidHandlers[pid]
		delete(d.pidHandlers, pid)
		d.reg.del(handler)
		log.Infof("unregistered worker: %v", handler)

		d.sendDispatchersMap()
	}
}

func (d *dispatcher) makeDispatchersMap() map[string]map[string]string {
	dispatchers := make(map[string]map[string]string)
	for handler, worker := range d.reg.all() {
		dispatchers[handler] = worker.features
	}

	return dispatchers
}

func (d *dispatcher) sendDispatchersMap() {
	d.dispatchers <- d.makeDispatchersMap()
}
