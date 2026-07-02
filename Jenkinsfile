// semidx — lean CI pipeline (Jenkins @ oracle-desktop, docker.sock local).
//
// SCOPE: validation gates only — gofmt, vet, build, test (-race) + coverage,
// golangci-lint, and a gitleaks secret scan. NO SonarQube quality gate, image
// build, Trivy scan or deploy yet: coverage lives only in internal/config for
// now (a hard gate would be red by design) and there is no server/image to ship
// until the product grows the `serve` command (roadmap phase F4). Those stages
// get added here when they become meaningful.
//
// Everything runs inside golang:1.25 (Debian) so the race detector's CGO
// requirement is satisfied without extra apt installs. Tool binaries are pinned
// via `go run <pkg>@<version>` so the pipeline needs no preinstalled tooling and
// the versions match go.mod / local dev.

pipeline {
  agent {
    docker {
      image 'golang:1.25'
      // -u root: the workspace is owned by the jenkins uid; running as root lets
      // Go write caches. GOCACHE/GOPATH go to /tmp so they never pollute the
      // workspace (the post-step chowns back what does — see the gotcha in
      // docs/CICD.md about root-owned leftovers breaking the next git clean).
      args '-u root -e GOCACHE=/tmp/.gocache -e GOPATH=/tmp/.gopath -e GOFLAGS=-buildvcs=false'
    }
  }

  options {
    timestamps()
    disableConcurrentBuilds()
    timeout(time: 25, unit: 'MINUTES')
    buildDiscarder(logRotator(numToKeepStr: '20'))
  }

  stages {
    stage('Format + Vet + Build + Test') {
      steps {
        sh '''
          set -e
          UNFMT=$(gofmt -l .)
          if [ -n "$UNFMT" ]; then
            echo "gofmt: files need formatting (run gofmt -w .):"; echo "$UNFMT"; exit 1
          fi
          go vet ./...
          go build ./...
          go test -race -shuffle=on -coverprofile=coverage.out ./...
          echo "--- coverage ---"; go tool cover -func=coverage.out | tail -1
        '''
      }
    }

    stage('Lint') {
      steps {
        sh 'go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run --timeout 5m ./...'
      }
    }

    stage('Secret scan') {
      steps {
        sh 'go run github.com/zricethezav/gitleaks/v8@latest detect --source . --no-banner --redact --exit-code 1'
      }
    }
  }

  post {
    always {
      // Stages ran as root; hand the workspace back to the jenkins uid so the
      // next build's git clean/checkout does not fail with "Operation not permitted".
      sh 'chown -R 1000:1000 "$WORKSPACE" 2>/dev/null || true'
    }
  }
}
