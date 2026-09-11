package web

import "testing"

func TestRegistryNamespaceTree(t *testing.T) {
	nodes := registryNamespaceTree([]string{"jiangnan/main/server-gate", "jiangnan/main/nested/api", "jiangnan/mainly/api", "jiangnan/dev/api", "root-image"}, "jiangnan/main/", "/resources/registries?connection=test")
	if len(nodes) != 1 || nodes[0].Name != "jiangnan" || !nodes[0].Open {
		t.Fatalf("root: %+v", nodes)
	}
	children := nodes[0].Children
	if len(children) != 3 || children[0].Name != "dev" || children[1].Name != "main" {
		t.Fatalf("children: %+v", children)
	}
	if !children[1].Selected || !children[1].Open || children[2].Open || children[2].Selected {
		t.Fatal("selection leaked to sibling prefix")
	}
	if len(children[1].Children) != 1 || children[1].Children[0].Path != "jiangnan/main/nested/" {
		t.Fatal("lost nested namespace")
	}
}
