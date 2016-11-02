package main

import (
	"log"
	"net/http"
	"time"

	"gopkg.in/mgo.v2"

	"encoding/json"

	"reflect"

	"github.com/gorilla/context"
	"github.com/julienschmidt/httprouter"
	"github.com/justinas/alice"
	"gopkg.in/mgo.v2/bson"
)

var (
	ErrBadRequest           = &Error{"bad_request", 400, "Bad request", "Request body is not well-formed. It must be JSON."}
	ErrNotAcceptable        = &Error{"not_acceptable", 406, "Not Acceptable", "Accept header must be so to 'application/vnd.api+json'."}
	ErrInternalServer       = &Error{"internal_server_error", 500, "Internal Server Error", "something went wrong."}
	ErrUnsupportedMediaType = &Error{"unsupported_media_type", 415, "Unsupported Media Type", "Content-Type header must be set to: 'application/vnd.api+json'."}
)

type Tea struct {
	Id       bson.ObjectId `json:"id,omitempty" bson:"_id,omitempty"`
	Name     string        `json:"name"`
	Category string        `json:"category"`
}

type TeaResource struct {
	Data Tea `json:"data"`
}

type TeaRepo struct {
	coll *mgo.Collection
}

type Errors struct {
	Errors []*Error `json:"errors"`
}

type Error struct {
	Id     string `json:"id"`
	Status int    `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

func writeError(w http.ResponseWriter, err *Error) {
	w.Header().Set("Content-type", "application/vnd.api+json")
	w.WriteHeader(err.Status)
	json.NewEncoder(w).Encode(Errors{[]*Error{err}})
}

func (r *TeaRepo) Find(id string) (TeaResource, error) {
	result := TeaResource{}
	err := r.coll.FindId(bson.ObjectIdHex(id)).One(&result.Data)
	if err != nil {
		return result, err
	}
	return result, nil
}

func (r *TeaRepo) Create(tea *Tea) error {
	id := bson.NewObjectId()
	_, err := r.coll.UpsertId(id, tea)
	if err != nil {
		return err
	}
	tea.Id = id
	return nil
}

func (c *appContext) teaHandler(w http.ResponseWriter, r *http.Request) {
	params := context.Get(r, "params").(httprouter.Params)
	repo := TeaRepo{c.db.C("teas")}
	tea, err := repo.Find(params.ByName("id"))
	if err != nil {
		panic(err)
	}
	w.Header().Set("Content-Type", "application/vnd.api+json")
	json.NewEncoder(w).Encode(tea)
}

func (c *appContext) createTeaHandler(w http.ResponseWriter, r *http.Request) {
	repo := TeaRepo{c.db.C("teas")}
	body := context.Get(r, "body").(*TeaResource)
	err := repo.Create(&body.Data)
	if err != nil {
		panic(err)
	}
	w.Header().Set("Content-Type", "application/vnd.api+json")
	w.WriteHeader(201)
	json.NewEncoder(w).Encode(body)

}
func loggingHandler(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		t1 := time.Now()
		next.ServeHTTP(w, r)
		t2 := time.Now()
		log.Printf("[%s] %q %v\n", r.Method, r.URL.String(), t2.Sub(t1))
	}
	return http.HandlerFunc(fn)
}

func recoverHandler(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("panic: %+v", err)
				writeError(w, ErrInternalServer)
			}
		}()
		next.ServeHTTP(w, r)
	}
	return http.HandlerFunc(fn)
}

type router struct {
	*httprouter.Router
}

type appContext struct {
	db *mgo.Database
}

func newRouter() *router {
	return &router{httprouter.New()}
}

func wrapHandler(h http.Handler) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		context.Set(r, "params", ps)
		h.ServeHTTP(w, r)
	}
}

func (r *router) get(path string, handler http.Handler) {
	r.GET(path, wrapHandler(handler))
}

func (r *router) post(path string, handler http.Handler) {
	r.POST(path, wrapHandler(handler))
}

func acceptHandler(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.api+json" {
			writeError(w, ErrNotAcceptable)
			return
		}
		next.ServeHTTP(w, r)
	}
	return http.HandlerFunc(fn)
}

func contentTypeHandler(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/vnd.api+json" {
			writeError(w, ErrUnsupportedMediaType)
			return
		}
		next.ServeHTTP(w, r)
	}
	return http.HandlerFunc(fn)
}

func bodyParserHandler(v interface{}) func(next http.Handler) http.Handler {
	t := reflect.TypeOf(v)
	m := func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			v := reflect.New(t).Interface()
			err := json.NewDecoder(r.Body).Decode(&v)
			if err != nil {
				writeError(w, ErrBadRequest)
				return
			}
			context.Set(r, "body", v)
			next.ServeHTTP(w, r)
		}
		return http.HandlerFunc(fn)
	}
	return m
}

func main() {
	session, err := mgo.Dial("localhost")
	if err != nil {
		panic(err)
	}
	defer session.Close()
	session.SetMode(mgo.Monotonic, true)

	appC := appContext{session.DB("test")}
	commonHandlers := alice.New(context.ClearHandler, loggingHandler, recoverHandler, acceptHandler)
	router := newRouter()
	router.post("/teas", commonHandlers.Append(contentTypeHandler, bodyParserHandler(TeaResource{})).ThenFunc(appC.createTeaHandler))
	router.get("/teas/:id", commonHandlers.ThenFunc(appC.teaHandler))
	http.ListenAndServe(":8080", router)
}
