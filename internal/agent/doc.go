// Package agent is hlab-agent, the agent inside the appliance. It applies
// the data plane's pictures, each as a whole, announces the appliance's
// addresses when a link comes up, and runs the metadata service. It decides
// nothing: what the appliance does comes from hlabd's pictures.
//
// It runs on Linux only; on other systems the package is empty.
package agent
