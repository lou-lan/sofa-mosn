package tcp

import (
	"gitlab.alipay-inc.com/afe/mosn/pkg/api/v2"
	"gitlab.alipay-inc.com/afe/mosn/pkg/types"
	"gitlab.alipay-inc.com/afe/mosn/pkg/network"
	"reflect"
)

// ReadFilter
type proxy struct {
	config              ProxyConfig
	clusterManager      types.ClusterManager
	readCallbacks       types.ReadFilterCallbacks
	upstreamConnection  types.ClientConnection
	requestInfo         types.RequestInfo
	upstreamCallbacks   UpstreamCallbacks
	downstreamCallbacks DownstreamCallbacks
}

func NewProxy(config *v2.TcpProxy, clusterManager types.ClusterManager) Proxy {
	proxy := &proxy{
		config:         NewProxyConfig(config),
		clusterManager: clusterManager,
		requestInfo:    network.NewRequestInfo(),
	}

	proxy.upstreamCallbacks = &upstreamCallbacks{
		proxy: proxy,
	}
	proxy.downstreamCallbacks = &downstreamCallbacks{
		proxy: proxy,
	}

	return proxy
}

func (p *proxy) OnData(buffer types.IoBuffer) types.FilterStatus {
	bytesRecved := p.requestInfo.BytesReceived() + uint64(buffer.Len())
	p.requestInfo.SetBytesReceived(bytesRecved)

	p.upstreamConnection.Write(buffer)

	return types.StopIteration
}

func (p *proxy) OnNewConnection() types.FilterStatus {
	return p.initializeUpstreamConnection()
}

func (p *proxy) InitializeReadFilterCallbacks(cb types.ReadFilterCallbacks) {
	p.readCallbacks = cb

	p.readCallbacks.Connection().AddConnectionCallbacks(p.downstreamCallbacks)

	p.requestInfo.SetDownstreamLocalAddress(p.readCallbacks.Connection().LocalAddr())
	p.requestInfo.SetDownstreamRemoteAddress(p.readCallbacks.Connection().RemoteAddr())

	p.readCallbacks.Connection().SetReadDisable(true)

	// TODO: set downstream connection stats
}

func (p *proxy) initializeUpstreamConnection() types.FilterStatus {
	clusterName := p.getUpstreamCluster()

	clusterSnapshot := p.clusterManager.Get(clusterName, nil)

	if reflect.ValueOf(clusterSnapshot).IsNil() {
		p.requestInfo.SetResponseFlag(types.NoRouteFound)
		p.onInitFailure(NoRoute)

		return types.StopIteration
	}

	clusterInfo := clusterSnapshot.ClusterInfo()
	clusterConnectionResource := clusterInfo.ResourceManager().ConnectionResource()

	if !clusterConnectionResource.CanCreate() {
		p.requestInfo.SetResponseFlag(types.UpstreamOverflow)
		p.onInitFailure(ResourceLimitExceeded)

		return types.StopIteration
	}

	connectionData := p.clusterManager.TcpConnForCluster(clusterName, nil)

	if connectionData.Connection == nil {
		p.requestInfo.SetResponseFlag(types.NoHealthyUpstream)
		p.onInitFailure(NoHealthyUpstream)

		return types.StopIteration
	}

	p.readCallbacks.SetUpstreamHost(connectionData.HostInfo)
	clusterConnectionResource.Increase()

	upstreamConnection := connectionData.Connection
	upstreamConnection.AddConnectionCallbacks(p.upstreamCallbacks)
	upstreamConnection.FilterManager().AddReadFilter(p.upstreamCallbacks)
	p.upstreamConnection = upstreamConnection

	upstreamConnection.Connect()

	p.requestInfo.OnUpstreamHostSelected(connectionData.HostInfo)

	// TODO: update upstream stats

	return types.Continue
}

func (p *proxy) closeUpstreamConnection() {
	// TODO: finalize upstream connection stats
	p.upstreamConnection.Close(types.NoFlush, types.LocalClose)
}

func (p *proxy) getUpstreamCluster() string {
	downstreamConnection := p.readCallbacks.Connection()

	return p.config.GetRouteFromEntries(downstreamConnection)
}

func (p *proxy) onInitFailure(reason UpstreamFailureReason) {
	p.readCallbacks.Connection().Close(types.NoFlush, types.LocalClose)
}

func (p *proxy) onUpstreamData(buffer types.IoBuffer) {
	bytesSent := p.requestInfo.BytesSent() + uint64(buffer.Len())
	p.requestInfo.SetBytesSent(bytesSent)

	p.readCallbacks.Connection().Write(buffer)
}

func (p *proxy) onUpstreamEvent(event types.ConnectionEvent) {
	switch event {
	case types.RemoteClose:
		p.finalizeUpstreamConnectionStats()
		p.readCallbacks.Connection().Close(types.FlushWrite, types.LocalClose)

	case types.LocalClose:
		p.finalizeUpstreamConnectionStats()
	case types.OnConnect:
	case types.Connected:
		p.readCallbacks.Connection().SetReadDisable(false)

		p.onConnectionSuccess()
	case types.ConnectTimeout:
		p.finalizeUpstreamConnectionStats()

		p.requestInfo.SetResponseFlag(types.UpstreamConnectionFailure)
		p.closeUpstreamConnection()
		p.initializeUpstreamConnection()
	}
}

func (p *proxy) finalizeUpstreamConnectionStats() {
	upstreamClusterInfo := p.readCallbacks.UpstreamHost().ClusterInfo()
	upstreamClusterInfo.ResourceManager().ConnectionResource().Decrease()
}

func (p *proxy) onConnectionSuccess() {}

func (p *proxy) onDownstreamEvent(event types.ConnectionEvent) {
	if p.upstreamConnection != nil {
		if event == types.RemoteClose {
			p.upstreamConnection.Close(types.FlushWrite, types.LocalClose)
		} else if event == types.LocalClose {
			p.upstreamConnection.Close(types.NoFlush, types.LocalClose)
		}
	}
}

func (p *proxy) ReadDisableUpstream(disable bool) {
	// TODO
}

func (p *proxy) ReadDisableDownstream(disable bool) {
	// TODO
}

type proxyConfig struct {
	routes []*route
}

type route struct {
	sourceAddrs      types.Addresses
	destinationAddrs types.Addresses
	clusterName      string
}

func NewProxyConfig(config *v2.TcpProxy) ProxyConfig {
	var routes []*route

	for _, routeConfig := range config.Routes {
		route := &route{
			clusterName:      routeConfig.Cluster,
			sourceAddrs:      routeConfig.SourceAddrs,
			destinationAddrs: routeConfig.DestinationAddrs,
		}

		routes = append(routes, route)
	}

	return &proxyConfig{
		routes: routes,
	}
}

func (pc *proxyConfig) GetRouteFromEntries(connection types.Connection) string {
	for _, r := range pc.routes {
		if len(r.sourceAddrs) != 0 && !r.sourceAddrs.Contains(connection.RemoteAddr()) {
			continue
		}

		if len(r.destinationAddrs) != 0 && r.destinationAddrs.Contains(connection.LocalAddr()) {
			continue
		}

		return r.clusterName
	}

	return ""
}

// ConnectionCallbacks
// ReadFilter
type upstreamCallbacks struct {
	proxy *proxy
}

func (uc *upstreamCallbacks) OnEvent(event types.ConnectionEvent) {
	switch event {
	case types.Connected:
		uc.proxy.upstreamConnection.SetNoDelay(true)
		uc.proxy.upstreamConnection.SetReadDisable(false)
	}

	uc.proxy.onUpstreamEvent(event)
}

func (uc *upstreamCallbacks) OnAboveWriteBufferHighWatermark() {
	// TODO
}

func (uc *upstreamCallbacks) OnBelowWriteBufferLowWatermark() {
	// TODO
}

func (uc *upstreamCallbacks) OnData(buffer types.IoBuffer) types.FilterStatus {
	uc.proxy.onUpstreamData(buffer)

	return types.StopIteration
}

func (uc *upstreamCallbacks) OnNewConnection() types.FilterStatus {
	return types.Continue
}

func (uc *upstreamCallbacks) InitializeReadFilterCallbacks(cb types.ReadFilterCallbacks) {}

// ConnectionCallbacks
type downstreamCallbacks struct {
	proxy *proxy
}

func (dc *downstreamCallbacks) OnEvent(event types.ConnectionEvent) {
	dc.proxy.onDownstreamEvent(event)
}

func (dc *downstreamCallbacks) OnAboveWriteBufferHighWatermark() {
	// TODO
}

func (dc *downstreamCallbacks) OnBelowWriteBufferLowWatermark() {
	// TODO
}
