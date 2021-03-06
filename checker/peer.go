package checker

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rancher/connectivity-check/utils"
	"github.com/rancher/go-rancher-metadata/metadata"
	"github.com/rancher/log"
)

// Peer is used to hold information about remote containers in
// the same service
type Peer struct {
	sync.Mutex
	uuid              string
	host              *metadata.Host
	container         *metadata.Container
	ccContainer       *metadata.Container
	exit              chan bool
	count             int
	random            *rand.Rand
	checkInterval     int
	connectionTimeout int
	lastChecked       time.Time
}

func (p *Peer) setupRandom() {
	n := time.Now().UTC().UnixNano()
	if p.host != nil {
		numFromIPStr := strings.Replace(p.host.AgentIP, ".", "", -1)
		numFromIP, err := strconv.ParseInt(numFromIPStr, 10, 64)
		if err != nil {
			log.Errorf("Peer(%v) couldn't convert to int: %v", p.uuid, numFromIPStr)
		}
		n = numFromIP
	}
	rs := rand.NewSource(n)
	p.random = rand.New(rs)
}

// Start is used to start the checker for a peer
func (p *Peer) Start() error {
	p.setupRandom()
	go p.Run()
	return nil
}

func (p *Peer) getHostCheckSleepDuration() time.Duration {
	r := p.checkInterval - p.random.Intn(1000)
	return (time.Duration(r) * time.Millisecond)
}

func (p *Peer) getHostIP() string {
	if p.host != nil {
		return p.host.AgentIP
	}
	return ""
}

// Run does the actual work
func (p *Peer) Run() {
	for {
		select {
		case _, ok := <-p.exit:
			if !ok {
				log.Infof("Peer: %v deleted, stopping check", p.uuid)
				return
			}
		default:
			p.doWork()
		}

		sleepFor := p.getHostCheckSleepDuration()
		log.Debugf("Peer(%v): sleeping for %v", p.uuid, sleepFor)
		time.Sleep(sleepFor)
	}
}

func (p *Peer) updateFailure() {
	if p.count > 0 {
		p.count--
		if p.count == 0 {
			log.Errorf("Peer(%v, %v, %v): became unreachable", p.uuid, p.getHostIP(), p.container.PrimaryIp)
		}
	}
	p.lastChecked = time.Now()
}

// UpdateFailure keeps track of failure count
func (p *Peer) UpdateFailure() {
	p.Lock()
	defer p.Unlock()
	p.updateFailure()
}

func (p *Peer) updateSuccess() {
	if p.count < 3 {
		p.count++
		if p.count == 1 {
			log.Infof("Peer(%v, %v, %v): became reachable", p.uuid, p.getHostIP(), p.container.PrimaryIp)
		}
	}
	p.lastChecked = time.Now()
}

// UpdateSuccess keeps track of success count
func (p *Peer) UpdateSuccess() {
	p.Lock()
	defer p.Unlock()
	p.updateSuccess()
}

func (p *Peer) doWork() error {
	p.Lock()
	defer p.Unlock()

	if !p.consider() {
		log.Debugf("Peer(%v): not considered", p.uuid)
		return nil
	}

	if !p.isItTimeToCheck() {
		log.Debugf("Peer(%v): skipping check", p.uuid)
		return nil
	}

	url := fmt.Sprintf("http://%v/ping", p.container.PrimaryIp)
	ok, err := utils.IsReachable(url, "pong", p.connectionTimeout)
	if ok {
		p.updateSuccess()
	} else {
		p.updateFailure()
	}
	if err != nil {
		log.Debugf("Peer(%v): checking reachability got err=%v", p.uuid, err)
	}
	return nil
}

func (p *Peer) isItTimeToCheck() bool {
	checkInterval := time.Duration(p.checkInterval) * time.Millisecond
	timeSinceLastChecked := time.Now().Sub(p.lastChecked)
	log.Debugf("Peer(%v): timeSinceLastChecked: %v (checkInterval: %v)", p.uuid, timeSinceLastChecked, checkInterval)
	if timeSinceLastChecked < checkInterval {
		return false
	}
	return true
}

func (p *Peer) consider() bool {
	if p.host == nil || p.container == nil || p.ccContainer == nil {
		log.Debugf("Peer(%v): host is not in considerable state p.host=%v p.container=%v p.ccContainer=%v", p.uuid, p.host, p.container, p.ccContainer)
		return false
	}
	log.Debugf("Peer(%v, %v, %v): host State=%v AgentState=%v", p.uuid, p.getHostIP(), p.container.PrimaryIp, p.host.State, p.host.AgentState)
	if !(p.host.State == "active") ||
		!(p.host.AgentState == "" || p.host.AgentState == "active") {
		log.Debugf("Peer(%v, %v, %v): host is not in considerable state", p.uuid, p.getHostIP(), p.container.PrimaryIp)
		return false
	}

	log.Debugf("Peer(%v, %v, %v): ccContainer.State=%v", p.uuid, p.getHostIP(), p.container.PrimaryIp, p.ccContainer.State)
	if p.ccContainer.State != "running" {
		log.Debugf("Peer(%v, %v, %v): ccContainer is not in considerable state (running)", p.uuid, p.getHostIP(), p.container.PrimaryIp)
		return false
	}

	log.Debugf("Peer(%v, %v, %v): container.State=%v", p.uuid, p.getHostIP(), p.container.PrimaryIp, p.container.State)
	if p.container.State != "running" {
		log.Debugf("Peer(%v, %v, %v): container is not in considerable state (running)", p.uuid, p.getHostIP(), p.container.PrimaryIp)
		return false
	}

	return true
}

// Consider informs if the Peer should be considered for doing checks
func (p *Peer) Consider() bool {
	p.Lock()
	defer p.Unlock()
	return p.consider()
}

// Shutdown is used to stop check for a peer
func (p *Peer) Shutdown() error {
	close(p.exit)
	return nil
}
