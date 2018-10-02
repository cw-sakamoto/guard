package server

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/appscode/guard/auth"
	"github.com/appscode/guard/auth/providers/appscode"
	"github.com/appscode/guard/auth/providers/azure"
	"github.com/appscode/guard/auth/providers/github"
	"github.com/appscode/guard/auth/providers/gitlab"
	"github.com/appscode/guard/auth/providers/google"
	"github.com/appscode/guard/auth/providers/ldap"
	"github.com/appscode/guard/auth/providers/token"
	"github.com/golang/glog"
	"github.com/pkg/errors"
	authv1 "k8s.io/api/authentication/v1"
)

var reqID int64

func (s Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	rid := atomic.AddInt64(&reqID, 1)
	start := time.Now()
	fmt.Printf("%s %s: (%v) [%s %s]\n", req.Method, req.RequestURI, rid, req.UserAgent(), req.RemoteAddr)
	defer func() {
		latency := time.Now().Sub(start)
		fmt.Printf("%s %s: (%v) %v [%s %s]\n", req.Method, req.RequestURI, rid, latency, req.UserAgent(), req.RemoteAddr)
	}()

	if req.TLS == nil || len(req.TLS.PeerCertificates) == 0 {
		write(w, nil, WithCode(errors.New("Missing client certificate"), http.StatusBadRequest))
		return
	}
	crt := req.TLS.PeerCertificates[0]
	if len(crt.Subject.Organization) == 0 {
		write(w, nil, WithCode(errors.New("Client certificate is missing organization"), http.StatusBadRequest))
		return
	}
	org := crt.Subject.Organization[0]
	glog.Infof("Received token review request for %s/%s", org, crt.Subject.CommonName)

	data := authv1.TokenReview{}
	err := json.NewDecoder(req.Body).Decode(&data)
	if err != nil {
		write(w, nil, WithCode(errors.Wrap(err, "Failed to parse request"), http.StatusBadRequest))
		return
	}

	if !s.RecommendedOptions.AuthProvider.Has(org) {
		write(w, nil, WithCode(errors.Errorf("guard does not provide service for %v", org), http.StatusBadRequest))
		return
	}

	if s.RecommendedOptions.AuthProvider.Has(token.OrgType) && s.TokenAuthenticator != nil {
		resp, err := s.TokenAuthenticator.Check(data.Spec.Token)
		if err == nil {
			write(w, resp, err)
			return
		}
	}

	client, err := s.getAuthProviderClient(org, crt.Subject.CommonName)
	if err != nil {
		write(w, nil, err)
		return
	}

	resp, err := client.Check(data.Spec.Token)
	write(w, resp, err)
	return
}

func (s Server) getAuthProviderClient(org, commonName string) (auth.Interface, error) {
	switch strings.ToLower(org) {
	case github.OrgType:
		return github.New(s.RecommendedOptions.Github, commonName), nil
	case google.OrgType:
		return google.New(s.RecommendedOptions.Google, commonName)
	case appscode.OrgType:
		return appscode.New(commonName), nil
	case gitlab.OrgType:
		return gitlab.New(s.RecommendedOptions.Gitlab), nil
	case azure.OrgType:
		return azure.New(s.RecommendedOptions.Azure)
	case ldap.OrgType:
		return ldap.New(s.RecommendedOptions.LDAP), nil
	}

	return nil, errors.Errorf("Client is using unknown organization %s", org)
}
