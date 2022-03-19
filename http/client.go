package client

import (
	"fmt"
	"io/ioutil"
	"math"
	"math/rand"
	"net/http"
	"os"
	"strconv"

	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"gopkg.in/yaml.v2"

	"github.com/Revolyssup/envoy-xds-server/apis/v1alpha1"
	"github.com/Revolyssup/envoy-xds-server/internal/resources"
	"github.com/Revolyssup/envoy-xds-server/internal/xdscache"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
)

//This server listens for HTTP request carrying envoy configs
type Server struct {
	Port   string
	Host   string
	cache  cache.SnapshotCache
	nodeID string

	// snapshotVersion holds the current version of the snapshot.
	snapshotVersion int64

	xdsCache xdscache.XDSCache
}

func (s *Server) Run() error {
	return http.ListenAndServe(s.Host+":"+s.Port, s)
}
func NewServer(host string, port string, nodeID string, cache cache.SnapshotCache) *Server {
	return &Server{
		Port:            port,
		Host:            host,
		cache:           cache,
		nodeID:          nodeID,
		snapshotVersion: rand.Int63n(1000),
		xdsCache: xdscache.XDSCache{
			Listeners: make(map[string]resources.Listener),
			Clusters:  make(map[string]resources.Cluster),
			Routes:    make(map[string]resources.Route),
			Endpoints: make(map[string]resources.Endpoint),
		},
	}
}
func (s Server) ServeHTTP(w http.ResponseWriter, rw *http.Request) {
	if rw.Method != http.MethodPost {
		http.Error(w, "Only post request allowed", http.StatusBadRequest)
		return
	}
	body, err := ioutil.ReadAll(rw.Body)
	if err != nil {
		http.Error(w, "could not read body", http.StatusBadRequest)
		return
	}
	var envoyConfig v1alpha1.EnvoyConfig
	err = yaml.Unmarshal(body, &envoyConfig)
	if err != nil {
		fmt.Println("recieved ", string(body))
		http.Error(w, "invalid envoy configuration", http.StatusBadRequest)
		return
	}
	// Parse Listeners
	for _, l := range envoyConfig.Listeners {
		var lRoutes []string
		for _, lr := range l.Routes {
			lRoutes = append(lRoutes, lr.Name)
		}

		s.xdsCache.AddListener(l.Name, lRoutes, l.Address, l.Port)

		for _, r := range l.Routes {
			s.xdsCache.AddRoute(r.Name, r.Prefix, r.ClusterNames)
		}
	}

	// Parse Clusters
	for _, c := range envoyConfig.Clusters {
		s.xdsCache.AddCluster(c.Name)

		// Parse endpoints
		for _, e := range c.Endpoints {
			s.xdsCache.AddEndpoint(c.Name, e.Address, e.Port)
		}
	}

	// Create the snapshot that we'll serve to Envoy
	snapshot := cache.NewSnapshot(
		s.newSnapshotVersion(),         // version
		s.xdsCache.EndpointsContents(), // endpoints
		s.xdsCache.ClusterContents(),   // clusters
		s.xdsCache.RouteContents(),     // routes
		s.xdsCache.ListenerContents(),  // listeners
		[]types.Resource{},             // runtimes
		[]types.Resource{},             // secrets
	)

	if err := snapshot.Consistent(); err != nil {
		http.Error(w, "snapshot inconsistency: "+snapshot.Consistent().Error(), http.StatusBadRequest)
		return
	}
	fmt.Println("will serve snapshot", snapshot)

	// Add the snapshot to the cache
	if err := s.cache.SetSnapshot(s.nodeID, snapshot); err != nil {
		http.Error(w, "snapshot error: "+snapshot.Consistent().Error(), http.StatusBadRequest)
		os.Exit(1)
	}
}

// newSnapshotVersion increments the current snapshotVersion
// and returns as a string.
func (p *Server) newSnapshotVersion() string {

	// Reset the snapshotVersion if it ever hits max size.
	if p.snapshotVersion == math.MaxInt64 {
		p.snapshotVersion = 0
	}

	// Increment the snapshot version & return as string.
	p.snapshotVersion++
	return strconv.FormatInt(p.snapshotVersion, 10)
}
