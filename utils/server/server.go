package server

import (
	"fmt"
	"log"
	"net/http"

	"github.com/gorilla/mux"
	nats "github.com/nats-io/nats-server/v2/server"
	"github.com/rs/cors"

	"github.com/spaceuptech/space-cloud/config"
	"github.com/spaceuptech/space-cloud/model"
	"github.com/spaceuptech/space-cloud/modules/auth"
	"github.com/spaceuptech/space-cloud/modules/crud"
	"github.com/spaceuptech/space-cloud/modules/eventing"
	"github.com/spaceuptech/space-cloud/modules/filestore"
	"github.com/spaceuptech/space-cloud/modules/functions"
	"github.com/spaceuptech/space-cloud/modules/realtime"
	"github.com/spaceuptech/space-cloud/modules/static"
	"github.com/spaceuptech/space-cloud/modules/userman"
	"github.com/spaceuptech/space-cloud/utils"
	"github.com/spaceuptech/space-cloud/utils/admin"
	"github.com/spaceuptech/space-cloud/utils/graphql"
	"github.com/spaceuptech/space-cloud/utils/syncman"
)

// Server is the object which sets up the server and handles all server operations
type Server struct {
	nodeID         string
	router         *mux.Router
	routerSecure   *mux.Router
	auth           *auth.Module
	crud           *crud.Module
	user           *userman.Module
	file           *filestore.Module
	functions      *functions.Module
	realtime       *realtime.Module
	static         *static.Module
	adminMan       *admin.Manager
	nats           *nats.Server
	eventing       *eventing.Module
	configFilePath string
	syncMan        *syncman.Manager
	ssl            *config.SSL
	graphql        *graphql.Module
}

// New creates a new server instance
func New(nodeID string) (*Server, error) {
	r := mux.NewRouter()
	r2 := mux.NewRouter()

	// Create the fundamental modules
	c := crud.Init()

	adminMan := admin.New()
	syncMan := syncman.New()
	fn := functions.Init(syncMan)

	a := auth.Init(c, fn)

	// Initialise the eventing module and set the crud module hooks
	e := eventing.New(a, c, fn, adminMan, syncMan)

	c.SetHooks(&model.CrudHooks{
		Create: e.HandleCreateIntent,
		Update: e.HandleUpdateIntent,
		Delete: e.HandleDeleteIntent,
		Batch:  e.HandleBatchIntent,
		Stage:  e.HandleStage,
	})

	rt, err := realtime.Init(nodeID, e, a, c, fn, syncMan)
	if err != nil {
		return nil, err
	}

	s := static.Init()
	u := userman.Init(c, a)
	f := filestore.Init(a)
	graphqlMan := graphql.New(a, c, fn)

	fmt.Println("Creating a new server with id", nodeID)

	return &Server{nodeID: nodeID, router: r, routerSecure: r2, auth: a, crud: c,
		user: u, file: f, static: s, syncMan: syncMan, adminMan: adminMan,
		functions: fn, realtime: rt, configFilePath: utils.DefaultConfigFilePath,
		eventing: e, graphql: graphqlMan}, nil
}

// Start begins the server operations
func (s *Server) Start(disableMetrics bool) error {

	// Start the sync manager
	if err := s.syncMan.Start(s.nodeID, s.configFilePath, s.LoadConfig); err != nil {
		return err
	}

	// Anonymously collect usage metrics if not explicitly disabled
	if !disableMetrics {
		go s.RoutineMetrics()
	}

	// Allow cors
	corsObj := cors.New(cors.Options{
		AllowCredentials: true,
		AllowOriginFunc: func(s string) bool {
			return true
		},
		AllowedMethods: []string{"GET", "PUT", "POST", "DELETE"},
		AllowedHeaders: []string{"Authorization", "Content-Type"},
		ExposedHeaders: []string{"Authorization", "Content-Type"},
	})

	fmt.Println("Starting http server on port: " + utils.PortHTTP)

	if s.ssl != nil && s.ssl.Enabled {
		handler := corsObj.Handler(s.routerSecure)
		fmt.Println("Starting https server on port: " + utils.PortHTTPSecure)
		go func() {

			if err := http.ListenAndServeTLS(":"+utils.PortHTTPSecure, s.ssl.Crt, s.ssl.Key, handler); err != nil {
				fmt.Println("Error starting https server:", err)
			}
		}()
	}

	handler := corsObj.Handler(s.router)

	fmt.Println()
	fmt.Println("\t Hosting mission control on http://localhost:" + utils.PortHTTP + "/mission-control/")
	fmt.Println()

	fmt.Println("Space cloud is running on the specified ports :D")
	return http.ListenAndServe(":"+utils.PortHTTP, handler)
}

// SetConfig sets the config
func (s *Server) SetConfig(c *config.Config, isProd bool) {
	s.ssl = c.SSL
	s.syncMan.SetGlobalConfig(c)
	s.adminMan.SetEnv(isProd)
	s.adminMan.SetConfig(c.Admin)
}

// LoadConfig configures each module to to use the provided config
func (s *Server) LoadConfig(config *config.Config) error {

	if config.Projects != nil {

		p := config.Projects[0]

		// Always set the config of the crud module first
		// Set the configuration for the crud module
		if err := s.crud.SetConfig(p.Modules.Crud); err != nil {
			log.Println("Error in crud module config: ", err)
			return err
		}

		// Set the configuration for the auth module
		if err := s.auth.SetConfig(p.ID, p.Secret, p.Modules.Crud, p.Modules.FileStore, p.Modules.Functions); err != nil {
			log.Println("Error in auth module config: ", err)
			return err
		}

		// Set the configuration for the functions module
		s.functions.SetConfig(p.ID, p.Modules.Functions)

		// Set the configuration for the user management module
		s.user.SetConfig(p.Modules.Auth)

		// Set the configuration for the file storage module
		if err := s.file.SetConfig(p.Modules.FileStore); err != nil {
			log.Println("Error in files module config: ", err)
			return err
		}

		if err := s.eventing.SetConfig(p.ID, &p.Modules.Eventing); err != nil {
			log.Println("Error in eventing module config: ", err)
			return err
		}

		// Set the configuration for the realtime module
		if err := s.realtime.SetConfig(p.ID, p.Modules.Crud); err != nil {
			log.Println("Error in realtime module config: ", err)
			return err
		}

		// Set the configuration for static module
		if err := s.static.SetConfig(config.Static); err != nil {
			log.Println("Error in static module config", err)
			return err
		}

		// Set the configuration for the graphql module
		s.graphql.SetConfig(p.ID)
	}

	return nil
}

// SetConfigFilePath sets the config file path
func (s *Server) SetConfigFilePath(configFilePath string) {
	s.configFilePath = configFilePath
}
