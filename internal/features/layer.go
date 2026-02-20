package features

import (
	"fmt"
	"image/color"
	"sync"

	"pcb-tracer/internal/connector"
	"pcb-tracer/internal/image"
	"pcb-tracer/internal/netlist"
	"pcb-tracer/internal/trace"
	"pcb-tracer/internal/via"
	"pcb-tracer/pkg/geometry"
)

// DetectedFeaturesLayer manages all detected and manually-added features
// from both board sides, with support for bus assignment and color coding.
type DetectedFeaturesLayer struct {
	mu sync.RWMutex

	// All features indexed by ID
	features map[string]*FeatureRef

	// Features organized by type for efficient iteration
	vias   []string // Via feature IDs
	traces []string // Trace feature IDs

	// Confirmed vias (detected on both sides)
	confirmedVias    []string                     // Confirmed via IDs
	confirmedViasMap map[string]*via.ConfirmedVia // ID -> ConfirmedVia

	// Connectors (board edge contacts)
	connectors    []string                        // Connector IDs
	connectorsMap map[string]*connector.Connector // ID -> Connector

	// Electrical nets
	nets    []string                           // Net IDs
	netsMap map[string]*netlist.ElectricalNet // ID -> ElectricalNet

	// Bus definitions
	Buses map[string]*Bus

	// Layer display settings
	Opacity float64 // 0.0 - 1.0
	Visible bool

	// Selection state
	selected map[string]bool
}

// NewDetectedFeaturesLayer creates a new empty features layer.
func NewDetectedFeaturesLayer() *DetectedFeaturesLayer {
	return &DetectedFeaturesLayer{
		features:         make(map[string]*FeatureRef),
		vias:             make([]string, 0),
		traces:           make([]string, 0),
		confirmedVias:    make([]string, 0),
		confirmedViasMap: make(map[string]*via.ConfirmedVia),
		connectors:       make([]string, 0),
		connectorsMap:    make(map[string]*connector.Connector),
		nets:             make([]string, 0),
		netsMap:          make(map[string]*netlist.ElectricalNet),
		Buses:            make(map[string]*Bus),
		Opacity:          0.7, // Default 70% opacity
		Visible:          true,
		selected:         make(map[string]bool),
	}
}

// AddVia adds a via to the layer.
func (l *DetectedFeaturesLayer) AddVia(v via.Via) {
	l.mu.Lock()
	defer l.mu.Unlock()

	ref := &FeatureRef{
		Feature: ViaFeature{v},
		Color:   UnassignedColor,
	}
	l.features[v.ID] = ref
	l.vias = append(l.vias, v.ID)
}

// AddVias adds multiple vias to the layer.
func (l *DetectedFeaturesLayer) AddVias(vias []via.Via) {
	for _, v := range vias {
		l.AddVia(v)
	}
}

// AddTrace adds a trace to the layer.
func (l *DetectedFeaturesLayer) AddTrace(t trace.ExtendedTrace) {
	l.mu.Lock()
	defer l.mu.Unlock()

	ref := &FeatureRef{
		Feature: TraceFeature{t},
		Color:   UnassignedColor,
	}
	l.features[t.ID] = ref
	l.traces = append(l.traces, t.ID)
}

// AddTraces adds multiple traces to the layer.
func (l *DetectedFeaturesLayer) AddTraces(traces []trace.ExtendedTrace) {
	for _, t := range traces {
		l.AddTrace(t)
	}
}

// ClearVias removes all vias from the layer.
func (l *DetectedFeaturesLayer) ClearVias() {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, id := range l.vias {
		delete(l.features, id)
		delete(l.selected, id)
	}
	l.vias = l.vias[:0]
}

// RemoveTrace removes a single trace by ID.
func (l *DetectedFeaturesLayer) RemoveTrace(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, ok := l.features[id]; !ok {
		return false
	}
	delete(l.features, id)
	delete(l.selected, id)
	for i, tid := range l.traces {
		if tid == id {
			l.traces = append(l.traces[:i], l.traces[i+1:]...)
			break
		}
	}
	return true
}

// GetTraces returns all trace IDs.
func (l *DetectedFeaturesLayer) GetTraces() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	result := make([]string, len(l.traces))
	copy(result, l.traces)
	return result
}

// GetTraceFeature returns the trace feature for the given ID, or nil.
func (l *DetectedFeaturesLayer) GetTraceFeature(id string) *trace.ExtendedTrace {
	l.mu.RLock()
	defer l.mu.RUnlock()
	ref, ok := l.features[id]
	if !ok {
		return nil
	}
	if tf, ok := ref.Feature.(TraceFeature); ok {
		return &tf.ExtendedTrace
	}
	return nil
}

// GetAllTraces returns all traces in the layer.
func (l *DetectedFeaturesLayer) GetAllTraces() []trace.ExtendedTrace {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var result []trace.ExtendedTrace
	for _, id := range l.traces {
		if ref := l.features[id]; ref != nil {
			if tf, ok := ref.Feature.(TraceFeature); ok {
				result = append(result, tf.ExtendedTrace)
			}
		}
	}
	return result
}

// ClearTraces removes all traces from the layer.
func (l *DetectedFeaturesLayer) ClearTraces() {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, id := range l.traces {
		delete(l.features, id)
		delete(l.selected, id)
	}
	l.traces = l.traces[:0]
}

// Clear removes all features from the layer.
func (l *DetectedFeaturesLayer) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.features = make(map[string]*FeatureRef)
	l.vias = l.vias[:0]
	l.traces = l.traces[:0]
	l.confirmedVias = l.confirmedVias[:0]
	l.confirmedViasMap = make(map[string]*via.ConfirmedVia)
	l.connectors = l.connectors[:0]
	l.connectorsMap = make(map[string]*connector.Connector)
	l.nets = l.nets[:0]
	l.netsMap = make(map[string]*netlist.ElectricalNet)
	l.selected = make(map[string]bool)
}

// AddConfirmedVia adds a confirmed via to the layer.
func (l *DetectedFeaturesLayer) AddConfirmedVia(cv *via.ConfirmedVia) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.confirmedViasMap[cv.ID] = cv
	l.confirmedVias = append(l.confirmedVias, cv.ID)
}

// GetConfirmedVias returns all confirmed vias.
func (l *DetectedFeaturesLayer) GetConfirmedVias() []*via.ConfirmedVia {
	l.mu.RLock()
	defer l.mu.RUnlock()

	result := make([]*via.ConfirmedVia, 0, len(l.confirmedVias))
	for _, id := range l.confirmedVias {
		if cv := l.confirmedViasMap[id]; cv != nil {
			result = append(result, cv)
		}
	}
	return result
}

// GetConfirmedViaByID returns a confirmed via by ID.
func (l *DetectedFeaturesLayer) GetConfirmedViaByID(id string) *via.ConfirmedVia {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.confirmedViasMap[id]
}

// ClearConfirmedVias removes all confirmed vias from the layer.
func (l *DetectedFeaturesLayer) ClearConfirmedVias() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.confirmedVias = l.confirmedVias[:0]
	l.confirmedViasMap = make(map[string]*via.ConfirmedVia)
}

// RemoveConfirmedVia removes a confirmed via by ID.
func (l *DetectedFeaturesLayer) RemoveConfirmedVia(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, exists := l.confirmedViasMap[id]; !exists {
		return false
	}

	delete(l.confirmedViasMap, id)

	// Remove from list
	for i, cvid := range l.confirmedVias {
		if cvid == id {
			l.confirmedVias = append(l.confirmedVias[:i], l.confirmedVias[i+1:]...)
			break
		}
	}

	return true
}

// ConfirmedViaCount returns the number of confirmed vias.
func (l *DetectedFeaturesLayer) ConfirmedViaCount() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.confirmedVias)
}

// NextConfirmedViaNumber returns the next sequential confirmed via number.
func (l *DetectedFeaturesLayer) NextConfirmedViaNumber() int {
	l.mu.RLock()
	defer l.mu.RUnlock()

	maxNum := 0
	for _, id := range l.confirmedVias {
		var num int
		if _, err := fmt.Sscanf(id, "cvia-%d", &num); err == nil {
			if num > maxNum {
				maxNum = num
			}
		}
	}
	return maxNum + 1
}

// HitTestConfirmedVia finds the confirmed via at the given coordinates.
func (l *DetectedFeaturesLayer) HitTestConfirmedVia(x, y float64) *via.ConfirmedVia {
	l.mu.RLock()
	defer l.mu.RUnlock()

	for _, id := range l.confirmedVias {
		if cv := l.confirmedViasMap[id]; cv != nil {
			if cv.HitTest(x, y) {
				return cv
			}
		}
	}
	return nil
}

// GetViaByID returns a via by its ID.
func (l *DetectedFeaturesLayer) GetViaByID(id string) *via.Via {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if ref := l.features[id]; ref != nil {
		if vf, ok := ref.Feature.(ViaFeature); ok {
			v := vf.Via
			return &v
		}
	}
	return nil
}

// UpdateVia updates a via in the layer.
func (l *DetectedFeaturesLayer) UpdateVia(v via.Via) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if ref := l.features[v.ID]; ref != nil {
		ref.Feature = ViaFeature{v}
	}
}

// GetFeature returns a feature by ID.
func (l *DetectedFeaturesLayer) GetFeature(id string) *FeatureRef {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.features[id]
}

// AllFeatures returns all feature references.
func (l *DetectedFeaturesLayer) AllFeatures() []*FeatureRef {
	l.mu.RLock()
	defer l.mu.RUnlock()

	refs := make([]*FeatureRef, 0, len(l.features))
	for _, ref := range l.features {
		refs = append(refs, ref)
	}
	return refs
}

// GetAllVias returns all vias in the layer.
func (l *DetectedFeaturesLayer) GetAllVias() []via.Via {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var result []via.Via
	for _, id := range l.vias {
		if ref := l.features[id]; ref != nil {
			if vf, ok := ref.Feature.(ViaFeature); ok {
				result = append(result, vf.Via)
			}
		}
	}
	return result
}

// ViaCount returns the number of vias.
func (l *DetectedFeaturesLayer) ViaCount() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.vias)
}

// NextViaNumber returns the next sequential via number for ID generation.
// This finds the highest existing via number and returns one higher.
func (l *DetectedFeaturesLayer) NextViaNumber() int {
	l.mu.RLock()
	defer l.mu.RUnlock()

	maxNum := 0
	for _, id := range l.vias {
		var num int
		if _, err := fmt.Sscanf(id, "via-%d", &num); err == nil {
			if num > maxNum {
				maxNum = num
			}
		}
	}
	return maxNum + 1
}

// TraceCount returns the number of traces.
func (l *DetectedFeaturesLayer) TraceCount() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.traces)
}

// ViaCountBySide returns the number of vias on each side.
func (l *DetectedFeaturesLayer) ViaCountBySide() (front, back int) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	for _, id := range l.vias {
		if ref := l.features[id]; ref != nil {
			if ref.Feature.FeatureSide() == image.SideFront {
				front++
			} else {
				back++
			}
		}
	}
	return
}

// GetViasBySide returns all vias for the specified side.
func (l *DetectedFeaturesLayer) GetViasBySide(side image.Side) []via.Via {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var result []via.Via
	for _, id := range l.vias {
		if ref := l.features[id]; ref != nil {
			if vf, ok := ref.Feature.(ViaFeature); ok {
				if vf.Via.Side == side {
					result = append(result, vf.Via)
				}
			}
		}
	}
	return result
}

// RemoveVia removes a via by its ID.
func (l *DetectedFeaturesLayer) RemoveVia(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, exists := l.features[id]; !exists {
		return false
	}

	// Remove from features map
	delete(l.features, id)
	delete(l.selected, id)

	// Remove from vias list
	for i, vid := range l.vias {
		if vid == id {
			l.vias = append(l.vias[:i], l.vias[i+1:]...)
			break
		}
	}

	return true
}

// TraceCountBySide returns the number of traces on each side.
func (l *DetectedFeaturesLayer) TraceCountBySide() (front, back int) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	for _, id := range l.traces {
		if ref := l.features[id]; ref != nil {
			if ref.Feature.FeatureSide() == image.SideFront {
				front++
			} else {
				back++
			}
		}
	}
	return
}

// HitTest finds the feature at the given coordinates.
// Returns nil if no feature is at that location.
func (l *DetectedFeaturesLayer) HitTest(x, y float64) *FeatureRef {
	l.mu.RLock()
	defer l.mu.RUnlock()

	// Check vias first (they're smaller, more precise hits)
	for _, id := range l.vias {
		if ref := l.features[id]; ref != nil {
			if ref.Feature.HitTest(x, y) {
				return ref
			}
		}
	}

	// Then check traces
	for _, id := range l.traces {
		if ref := l.features[id]; ref != nil {
			if ref.Feature.HitTest(x, y) {
				return ref
			}
		}
	}

	return nil
}

// HitTestAll finds all features at the given coordinates.
func (l *DetectedFeaturesLayer) HitTestAll(x, y float64) []*FeatureRef {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var hits []*FeatureRef

	for _, ref := range l.features {
		if ref.Feature.HitTest(x, y) {
			hits = append(hits, ref)
		}
	}

	return hits
}

// Selection methods

// Select adds a feature to the selection.
func (l *DetectedFeaturesLayer) Select(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if ref := l.features[id]; ref != nil {
		l.selected[id] = true
		ref.Selected = true
	}
}

// Deselect removes a feature from the selection.
func (l *DetectedFeaturesLayer) Deselect(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if ref := l.features[id]; ref != nil {
		delete(l.selected, id)
		ref.Selected = false
	}
}

// ToggleSelect toggles the selection state of a feature.
func (l *DetectedFeaturesLayer) ToggleSelect(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if ref := l.features[id]; ref != nil {
		if l.selected[id] {
			delete(l.selected, id)
			ref.Selected = false
		} else {
			l.selected[id] = true
			ref.Selected = true
		}
	}
}

// ClearSelection deselects all features.
func (l *DetectedFeaturesLayer) ClearSelection() {
	l.mu.Lock()
	defer l.mu.Unlock()

	for id := range l.selected {
		if ref := l.features[id]; ref != nil {
			ref.Selected = false
		}
	}
	l.selected = make(map[string]bool)
}

// SelectedIDs returns the IDs of all selected features.
func (l *DetectedFeaturesLayer) SelectedIDs() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	ids := make([]string, 0, len(l.selected))
	for id := range l.selected {
		ids = append(ids, id)
	}
	return ids
}

// SelectedCount returns the number of selected features.
func (l *DetectedFeaturesLayer) SelectedCount() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.selected)
}

// Bus methods

// CreateBus creates a new bus with the given name.
func (l *DetectedFeaturesLayer) CreateBus(name string) *Bus {
	l.mu.Lock()
	defer l.mu.Unlock()

	bus := NewBus(name, len(l.Buses))
	l.Buses[bus.ID] = bus
	return bus
}

// GetBus returns a bus by ID.
func (l *DetectedFeaturesLayer) GetBus(id string) *Bus {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.Buses[id]
}

// DeleteBus removes a bus and unassigns all its features.
func (l *DetectedFeaturesLayer) DeleteBus(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	bus := l.Buses[id]
	if bus == nil {
		return
	}

	// Unassign all features from this bus
	for _, featureID := range bus.Features {
		if ref := l.features[featureID]; ref != nil {
			ref.BusID = ""
			ref.Color = UnassignedColor
		}
	}

	delete(l.Buses, id)
}

// AssignToBus assigns the selected features to a bus.
func (l *DetectedFeaturesLayer) AssignToBus(busID string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	bus := l.Buses[busID]
	if bus == nil {
		return
	}

	for id := range l.selected {
		if ref := l.features[id]; ref != nil {
			// Remove from previous bus if assigned
			if ref.BusID != "" {
				if oldBus := l.Buses[ref.BusID]; oldBus != nil {
					oldBus.Features = removeString(oldBus.Features, id)
				}
			}

			// Assign to new bus
			ref.BusID = busID
			ref.Color = bus.Color
			bus.Features = append(bus.Features, id)
		}
	}
}

// AssignSelectedToNewBus creates a new bus and assigns selected features to it.
func (l *DetectedFeaturesLayer) AssignSelectedToNewBus(name string) *Bus {
	bus := l.CreateBus(name)
	l.AssignToBus(bus.ID)
	return bus
}

// SetBusColor changes the color of a bus and all its features.
func (l *DetectedFeaturesLayer) SetBusColor(busID string, c color.RGBA) {
	l.mu.Lock()
	defer l.mu.Unlock()

	bus := l.Buses[busID]
	if bus == nil {
		return
	}

	bus.Color = c
	for _, featureID := range bus.Features {
		if ref := l.features[featureID]; ref != nil {
			ref.Color = c
		}
	}
}

// UnassignFromBus removes features from their bus assignments.
func (l *DetectedFeaturesLayer) UnassignFromBus(featureIDs []string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, id := range featureIDs {
		if ref := l.features[id]; ref != nil {
			if ref.BusID != "" {
				if bus := l.Buses[ref.BusID]; bus != nil {
					bus.Features = removeString(bus.Features, id)
				}
				ref.BusID = ""
				ref.Color = UnassignedColor
			}
		}
	}
}

// GetBusList returns all buses sorted by name.
func (l *DetectedFeaturesLayer) GetBusList() []*Bus {
	l.mu.RLock()
	defer l.mu.RUnlock()

	buses := make([]*Bus, 0, len(l.Buses))
	for _, bus := range l.Buses {
		buses = append(buses, bus)
	}
	return buses
}

// UnassignedCount returns the number of features not assigned to any bus.
func (l *DetectedFeaturesLayer) UnassignedCount() int {
	l.mu.RLock()
	defer l.mu.RUnlock()

	count := 0
	for _, ref := range l.features {
		if ref.BusID == "" {
			count++
		}
	}
	return count
}

// GetFeaturesInRegion returns all features whose bounds intersect the region.
func (l *DetectedFeaturesLayer) GetFeaturesInRegion(region geometry.RectInt) []*FeatureRef {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var hits []*FeatureRef

	for _, ref := range l.features {
		bounds := ref.Feature.GetBounds()
		if rectsIntersect(bounds, region) {
			hits = append(hits, ref)
		}
	}

	return hits
}

// Connector methods

// AddConnector adds a connector to the layer.
func (l *DetectedFeaturesLayer) AddConnector(c *connector.Connector) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.connectorsMap[c.ID] = c
	l.connectors = append(l.connectors, c.ID)
}

// GetConnectors returns all connectors.
func (l *DetectedFeaturesLayer) GetConnectors() []*connector.Connector {
	l.mu.RLock()
	defer l.mu.RUnlock()

	result := make([]*connector.Connector, 0, len(l.connectors))
	for _, id := range l.connectors {
		if c := l.connectorsMap[id]; c != nil {
			result = append(result, c)
		}
	}
	return result
}

// GetConnectorByID returns a connector by ID.
func (l *DetectedFeaturesLayer) GetConnectorByID(id string) *connector.Connector {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.connectorsMap[id]
}

// GetConnectorByPin returns the connector for a given pin number.
func (l *DetectedFeaturesLayer) GetConnectorByPin(pinNumber int, front bool) *connector.Connector {
	l.mu.RLock()
	defer l.mu.RUnlock()

	for _, c := range l.connectorsMap {
		isFront := c.Side == image.SideFront
		if c.PinNumber == pinNumber && isFront == front {
			return c
		}
	}
	return nil
}

// GetConnectorsBySide returns all connectors on the specified side.
func (l *DetectedFeaturesLayer) GetConnectorsBySide(side image.Side) []*connector.Connector {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var result []*connector.Connector
	for _, id := range l.connectors {
		if c := l.connectorsMap[id]; c != nil && c.Side == side {
			result = append(result, c)
		}
	}
	return result
}

// ClearConnectors removes all connectors from the layer.
func (l *DetectedFeaturesLayer) ClearConnectors() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.connectors = l.connectors[:0]
	l.connectorsMap = make(map[string]*connector.Connector)
}

// RemoveConnector removes a single connector by ID.
func (l *DetectedFeaturesLayer) RemoveConnector(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, exists := l.connectorsMap[id]; !exists {
		return false
	}

	delete(l.connectorsMap, id)

	for i, cid := range l.connectors {
		if cid == id {
			l.connectors = append(l.connectors[:i], l.connectors[i+1:]...)
			break
		}
	}

	return true
}

// ConnectorCount returns the number of connectors.
func (l *DetectedFeaturesLayer) ConnectorCount() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.connectors)
}

// HitTestConnector finds the connector at the given coordinates.
func (l *DetectedFeaturesLayer) HitTestConnector(x, y float64) *connector.Connector {
	l.mu.RLock()
	defer l.mu.RUnlock()

	for _, id := range l.connectors {
		if c := l.connectorsMap[id]; c != nil {
			if c.HitTest(x, y) {
				return c
			}
		}
	}
	return nil
}

// Electrical net methods

// AddNet adds an electrical net to the layer.
func (l *DetectedFeaturesLayer) AddNet(n *netlist.ElectricalNet) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.netsMap[n.ID] = n
	l.nets = append(l.nets, n.ID)
}

// GetNets returns all electrical nets.
func (l *DetectedFeaturesLayer) GetNets() []*netlist.ElectricalNet {
	l.mu.RLock()
	defer l.mu.RUnlock()

	result := make([]*netlist.ElectricalNet, 0, len(l.nets))
	for _, id := range l.nets {
		if n := l.netsMap[id]; n != nil {
			result = append(result, n)
		}
	}
	return result
}

// GetNetByID returns an electrical net by ID.
func (l *DetectedFeaturesLayer) GetNetByID(id string) *netlist.ElectricalNet {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.netsMap[id]
}

// GetNetByName returns an electrical net by name.
func (l *DetectedFeaturesLayer) GetNetByName(name string) *netlist.ElectricalNet {
	l.mu.RLock()
	defer l.mu.RUnlock()

	for _, n := range l.netsMap {
		if n.Name == name {
			return n
		}
	}
	return nil
}

// GetNetForElement returns the net containing an element.
func (l *DetectedFeaturesLayer) GetNetForElement(elementID string) *netlist.ElectricalNet {
	l.mu.RLock()
	defer l.mu.RUnlock()

	for _, n := range l.netsMap {
		if n.ContainsElement(elementID) {
			return n
		}
	}
	return nil
}

// ClearNets removes all electrical nets from the layer.
func (l *DetectedFeaturesLayer) ClearNets() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.nets = l.nets[:0]
	l.netsMap = make(map[string]*netlist.ElectricalNet)
}

// NetCount returns the number of electrical nets.
func (l *DetectedFeaturesLayer) NetCount() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.nets)
}

// RemoveNet removes an electrical net by ID.
func (l *DetectedFeaturesLayer) RemoveNet(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, exists := l.netsMap[id]; !exists {
		return false
	}

	delete(l.netsMap, id)

	for i, nid := range l.nets {
		if nid == id {
			l.nets = append(l.nets[:i], l.nets[i+1:]...)
			break
		}
	}

	return true
}

// UpdateTracePoints updates a trace's points in-place for vertex dragging.
func (l *DetectedFeaturesLayer) UpdateTracePoints(id string, points []geometry.Point2D) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	ref, ok := l.features[id]
	if !ok {
		return false
	}
	tf, ok := ref.Feature.(TraceFeature)
	if !ok {
		return false
	}
	tf.Points = points
	ref.Feature = tf
	return true
}

// Helper functions

func removeString(slice []string, s string) []string {
	for i, v := range slice {
		if v == s {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}

func rectsIntersect(a, b geometry.RectInt) bool {
	return a.X < b.X+b.Width &&
		a.X+a.Width > b.X &&
		a.Y < b.Y+b.Height &&
		a.Y+a.Height > b.Y
}
