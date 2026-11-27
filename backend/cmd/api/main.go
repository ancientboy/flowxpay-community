package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"flowxpay.local/platform/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

type sandboxMerchant struct {
	ID, Name, Key string
}

type server struct {
	platform          *platformPreview
	db                *sql.DB
	merchants         [2]sandboxMerchant
	signing           []byte
	mfaNow            func() time.Time
	collection        collectionAdapter
	collectionTimeout time.Duration
	events            eventConfig
	recovery          channelRecovery
	settlementSlots   [2]sync.Mutex
	requestKeys       merchantRequestKeys
}

func main() {
	if mode := os.Getenv("FLOWXPAY_MODE"); mode != "" && mode != "sandbox" {
		log.Fatal("FlowXPay only supports sandbox mode")
	}
	process := os.Getenv("FLOWXPAY_SANDBOX_PROCESS")
	switch process {
	case "", "api", "worker":
	default:
		log.Fatal("FLOWXPAY_SANDBOX_PROCESS must be api, worker, or unset")
	}
	primary := os.Getenv("FLOWXPAY_SANDBOX_KEY")
	secondary := os.Getenv("FLOWXPAY_SECONDARY_KEY")
	signing := os.Getenv("FLOWXPAY_CHECKOUT_SECRET")
	databaseURL := os.Getenv("FLOWXPAY_DATABASE_URL")
	if len(primary) < 32 || len(secondary) < 32 || len(signing) < 32 ||
		primary == secondary || primary == signing || secondary == signing || databaseURL == "" {
		log.Fatal("configure a sandbox database and three distinct secrets of at least 32 bytes")
	}
	address := os.Getenv("FLOWXPAY_LISTEN")
	if address == "" {
		address = "127.0.0.1:8091"
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		log.Fatal("the sandbox API requires a loopback address")
	}

	databaseConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil || !sandboxDatabaseHost(databaseConfig.Host) {
		log.Fatal("configure the sandbox database on a Unix socket or loopback address")
	}
	for _, fallback := range databaseConfig.Fallbacks {
		if !sandboxDatabaseHost(fallback.Host) {
			log.Fatal("remote sandbox database fallbacks are not supported")
		}
	}
	db := stdlib.OpenDB(*databaseConfig)
	defer db.Close()
	db.SetMaxOpenConns(12)
	db.SetMaxIdleConns(4)
	db.SetConnMaxIdleTime(time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := migrations.Apply(ctx, db); err != nil {
		log.Fatal("cannot initialize the sandbox database schema")
	}
	app := &server{db: db, signing: []byte(signing), merchants: [2]sandboxMerchant{
		{ID: "M10028", Name: "Cedar Commerce", Key: primary},
		{ID: "M20029", Name: "Harbor Retail", Key: secondary},
	}}
	app.platform, err = loadPlatformPreview()
	if err != nil {
		log.Fatal(err)
	}
	app.events, err = loadEventConfig(app)
	if err != nil {
		log.Fatal(err)
	}
	app.collection = sandboxCollection{db: db}
	app.requestKeys, err = loadMerchantRequestKeys(os.Getenv("FLOWXPAY_SANDBOX_REQUEST_PUBLIC_KEYS"))
	if err != nil {
		log.Fatal(err)
	}
	if len(app.requestKeys) != 0 && process != "worker" {
		go app.pruneMerchantRequestNonces()
	}
	if process == "worker" {
		workerContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		log.Print("FlowXPay sandbox event worker started")
		app.runEventWorker(workerContext)
		return
	}
	if process != "api" {
		go app.runEventWorker(context.Background())
	}
	httpServer := &http.Server{
		Addr: address, Handler: app.routes(), ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout: 8 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 45 * time.Second,
	}
	log.Printf("FlowXPay sandbox API listening on %s", address)
	if err := httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func sandboxDatabaseHost(host string) bool {
	ip := net.ParseIP(host)
	return strings.HasPrefix(host, "/") || (ip != nil && ip.IsLoopback())
}

type merchantHandler func(http.ResponseWriter, *http.Request, sandboxMerchant)

func (s *server) authorize(next merchantHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if ok && token != "" && len(r.Header.Values("Authorization")) == 1 {
			merchant, err := s.apiKeyMerchant(r.Context(), token)
			switch {
			case err == nil:
				next(w, r, merchant)
				return
			case !errors.Is(err, sql.ErrNoRows):
				fail(w, http.StatusServiceUnavailable, "database_unavailable", "Sandbox key authentication is unavailable.")
				return
			}
		}
		w.Header().Set("WWW-Authenticate", "Bearer")
		fail(w, http.StatusUnauthorized, "unauthorized", "A valid sandbox API key is required.")
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.db.PingContext(r.Context()); err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "The sandbox database is unavailable.")
			return
		}
		respond(w, http.StatusOK, map[string]string{"status": "ok", "environment": "sandbox"})
	})
	mux.HandleFunc("GET /v1/capabilities", s.authorize(s.capabilities))
	mux.HandleFunc("POST /v1/payments", s.authorize(s.requireSignedWrite(s.createPayment)))
	mux.HandleFunc("GET /v1/payments", s.authorize(s.listPayments))
	mux.HandleFunc("GET /v1/payments/{id}", s.authorize(s.getPayment))
	mux.HandleFunc("GET /sandbox/checkout/{token}", s.viewCheckout)
	mux.HandleFunc("POST /sandbox/checkout/{token}/confirm", s.confirmCheckout)
	mux.HandleFunc("GET /sandbox/checkout/{token}/qr", s.checkoutQR)
	s.registerEventRoutes(mux)
	s.registerMemberRoutes(mux)
	s.registerPlatformPreview(mux)
	s.registerWorkspaceControlRoutes(mux)
	s.registerPayoutRoutes(mux)
	s.registerSettlementRoutes(mux)
	s.registerWorkspaceAuthRoutes(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		mux.ServeHTTP(w, r)
	})
}
