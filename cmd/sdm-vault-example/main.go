package main

import (
	"github.com/StreamDockManager/Plugins/internal/engine"
	example "github.com/StreamDockManager/Plugins/internal/plugins/sdm-vault-example"
)

func main() { engine.Main(example.Definition, example.Factory) }
