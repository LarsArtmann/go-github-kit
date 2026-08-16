package githubkit_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGithubkit(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "go-github-kit Suite")
}
