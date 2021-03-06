package diff

import (
	"bytes"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"testing"

	"github.com/cli/cli/context"
	"github.com/cli/cli/git"
	"github.com/cli/cli/internal/config"
	"github.com/cli/cli/internal/ghrepo"
	"github.com/cli/cli/pkg/cmdutil"
	"github.com/cli/cli/pkg/httpmock"
	"github.com/cli/cli/pkg/iostreams"
	"github.com/cli/cli/test"
	"github.com/google/go-cmp/cmp"
	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
)

func runCommand(rt http.RoundTripper, remotes context.Remotes, isTTY bool, cli string) (*test.CmdOut, error) {
	io, _, stdout, stderr := iostreams.Test()
	io.SetStdoutTTY(isTTY)
	io.SetStdinTTY(isTTY)
	io.SetStderrTTY(isTTY)

	factory := &cmdutil.Factory{
		IOStreams: io,
		HttpClient: func() (*http.Client, error) {
			return &http.Client{Transport: rt}, nil
		},
		Config: func() (config.Config, error) {
			return config.NewBlankConfig(), nil
		},
		BaseRepo: func() (ghrepo.Interface, error) {
			return ghrepo.New("OWNER", "REPO"), nil
		},
		Remotes: func() (context.Remotes, error) {
			if remotes == nil {
				return context.Remotes{
					{
						Remote: &git.Remote{Name: "origin"},
						Repo:   ghrepo.New("OWNER", "REPO"),
					},
				}, nil
			}

			return remotes, nil
		},
		Branch: func() (string, error) {
			return "feature", nil
		},
	}

	cmd := NewCmdDiff(factory, nil)

	argv, err := shlex.Split(cli)
	if err != nil {
		return nil, err
	}
	cmd.SetArgs(argv)

	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(ioutil.Discard)
	cmd.SetErr(ioutil.Discard)

	_, err = cmd.ExecuteC()
	return &test.CmdOut{
		OutBuf: stdout,
		ErrBuf: stderr,
	}, err
}

func TestPRDiff_validation(t *testing.T) {
	_, err := runCommand(nil, nil, false, "--color=doublerainbow")
	if err == nil {
		t.Fatal("expected error")
	}
	assert.Equal(t, `did not understand color: "doublerainbow". Expected one of always, never, or auto`, err.Error())
}

func TestPRDiff_no_current_pr(t *testing.T) {
	http := &httpmock.Registry{}
	defer http.Verify(t)
	http.StubResponse(200, bytes.NewBufferString(`
		{ "data": { "repository": { "pullRequests": { "nodes": [] } } } }
	`))
	_, err := runCommand(http, nil, false, "")
	if err == nil {
		t.Fatal("expected error")
	}
	assert.Equal(t, `no open pull requests found for branch "feature"`, err.Error())
}

func TestPRDiff_argument_not_found(t *testing.T) {
	http := &httpmock.Registry{}
	defer http.Verify(t)
	http.StubResponse(200, bytes.NewBufferString(`
	{ "data": { "repository": {
		"pullRequest": { "number": 123 }
	} } }
`))
	http.StubResponse(404, bytes.NewBufferString(""))
	_, err := runCommand(http, nil, false, "123")
	if err == nil {
		t.Fatal("expected error", err)
	}
	assert.Equal(t, `could not find pull request diff: pull request not found`, err.Error())
}

func TestPRDiff_notty(t *testing.T) {
	http := &httpmock.Registry{}
	defer http.Verify(t)
	http.StubResponse(200, bytes.NewBufferString(`
		{ "data": { "repository": { "pullRequests": { "nodes": [
			{ "url": "https://github.com/OWNER/REPO/pull/123",
			  "number": 123,
			  "id": "foobar123",
			  "headRefName": "feature",
				"baseRefName": "master" }
		] } } } }`))
	http.StubResponse(200, bytes.NewBufferString(testDiff))
	output, err := runCommand(http, nil, false, "")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if diff := cmp.Diff(testDiff, output.String()); diff != "" {
		t.Errorf("command output did not match:\n%s", diff)
	}
}

func TestPRDiff_tty(t *testing.T) {
	pager := os.Getenv("PAGER")
	http := &httpmock.Registry{}
	defer func() {
		os.Setenv("PAGER", pager)
		http.Verify(t)
	}()
	os.Setenv("PAGER", "")
	http.StubResponse(200, bytes.NewBufferString(`
		{ "data": { "repository": { "pullRequests": { "nodes": [
			{ "url": "https://github.com/OWNER/REPO/pull/123",
			  "number": 123,
			  "id": "foobar123",
			  "headRefName": "feature",
				"baseRefName": "master" }
		] } } } }`))
	http.StubResponse(200, bytes.NewBufferString(testDiff))
	output, err := runCommand(http, nil, true, "")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	assert.Contains(t, output.String(), "\x1b[32m+site: bin/gh\x1b[m")
}

func TestPRDiff_pager(t *testing.T) {
	realRunPager := runPager
	pager := os.Getenv("PAGER")
	http := &httpmock.Registry{}
	defer func() {
		runPager = realRunPager
		os.Setenv("PAGER", pager)
		http.Verify(t)
	}()
	runPager = func(pager string, diff io.Reader, out io.Writer) error {
		_, err := io.Copy(out, diff)
		return err
	}
	os.Setenv("PAGER", "fakepager")
	http.StubResponse(200, bytes.NewBufferString(`
	{ "data": { "repository": { "pullRequests": { "nodes": [
		{ "url": "https://github.com/OWNER/REPO/pull/123",
		  "number": 123,
		  "id": "foobar123",
		  "headRefName": "feature",
			"baseRefName": "master" }
	] } } } }`))
	http.StubResponse(200, bytes.NewBufferString(testDiff))
	output, err := runCommand(http, nil, true, "")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if diff := cmp.Diff(testDiff, output.String()); diff != "" {
		t.Errorf("command output did not match:\n%s", diff)
	}
}

const testDiff = `diff --git a/.github/workflows/releases.yml b/.github/workflows/releases.yml
index 73974448..b7fc0154 100644
--- a/.github/workflows/releases.yml
+++ b/.github/workflows/releases.yml
@@ -44,6 +44,11 @@ jobs:
           token: ${{secrets.SITE_GITHUB_TOKEN}}
       - name: Publish documentation site
         if: "!contains(github.ref, '-')" # skip prereleases
+        env:
+          GIT_COMMITTER_NAME: cli automation
+          GIT_AUTHOR_NAME: cli automation
+          GIT_COMMITTER_EMAIL: noreply@github.com
+          GIT_AUTHOR_EMAIL: noreply@github.com
         run: make site-publish
       - name: Move project cards
         if: "!contains(github.ref, '-')" # skip prereleases
diff --git a/Makefile b/Makefile
index f2b4805c..3d7bd0f9 100644
--- a/Makefile
+++ b/Makefile
@@ -22,8 +22,8 @@ test:
 	go test ./...
 .PHONY: test
 
-site:
-	git clone https://github.com/github/cli.github.com.git "$@"
+site: bin/gh
+	bin/gh repo clone github/cli.github.com "$@"
 
 site-docs: site
 	git -C site pull
`
