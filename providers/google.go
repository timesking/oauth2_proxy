package providers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	admin "google.golang.org/api/admin/directory/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/script/v1"
)

type GoogleProvider struct {
	*ProviderData
	RedeemRefreshURL *url.URL
	// GroupValidator is a function that determines if the passed email is in
	// the configured Google group.
	GroupValidator func(*SessionState) bool
}

func NewGoogleProvider(p *ProviderData) *GoogleProvider {
	p.ProviderName = "Google"
	if p.LoginURL.String() == "" {
		p.LoginURL = &url.URL{Scheme: "https",
			Host: "accounts.google.com",
			Path: "/o/oauth2/auth",
			// to get a refresh token. see https://developers.google.com/identity/protocols/OAuth2WebServer#offline
			RawQuery: "access_type=offline",
		}
	}
	if p.RedeemURL.String() == "" {
		p.RedeemURL = &url.URL{Scheme: "https",
			Host: "www.googleapis.com",
			Path: "/oauth2/v3/token"}
	}
	if p.ValidateURL.String() == "" {
		p.ValidateURL = &url.URL{Scheme: "https",
			Host: "www.googleapis.com",
			Path: "/oauth2/v1/tokeninfo"}
	}
	if p.Scope == "" {
		p.Scope = "profile email"
	}

	return &GoogleProvider{
		ProviderData: p,
		// Set a default GroupValidator to just always return valid (true), it will
		// be overwritten if we configured a Google group restriction.
		GroupValidator: func(*SessionState) bool {
			return true
		},
	}
}

func emailFromIdToken(idToken string) (string, error) {

	// id_token is a base64 encode ID token payload
	// https://developers.google.com/accounts/docs/OAuth2Login#obtainuserinfo
	jwt := strings.Split(idToken, ".")
	jwtData := strings.TrimSuffix(jwt[1], "=")
	b, err := base64.RawURLEncoding.DecodeString(jwtData)
	if err != nil {
		return "", err
	}

	var email struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	err = json.Unmarshal(b, &email)
	if err != nil {
		return "", err
	}
	if email.Email == "" {
		return "", errors.New("missing email")
	}
	if !email.EmailVerified {
		return "", fmt.Errorf("email %s not listed as verified", email.Email)
	}
	return email.Email, nil
}

func (p *GoogleProvider) Redeem(redirectURL, code string) (s *SessionState, err error) {
	if code == "" {
		err = errors.New("missing code")
		return
	}

	params := url.Values{}
	params.Add("redirect_uri", redirectURL)
	params.Add("client_id", p.ClientID)
	params.Add("client_secret", p.ClientSecret)
	params.Add("code", code)
	params.Add("grant_type", "authorization_code")
	var req *http.Request
	req, err = http.NewRequest("POST", p.RedeemURL.String(), bytes.NewBufferString(params.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	var body []byte
	body, err = ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return
	}

	if resp.StatusCode != 200 {
		err = fmt.Errorf("got %d from %q %s", resp.StatusCode, p.RedeemURL.String(), body)
		return
	}

	var jsonResponse struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		IdToken      string `json:"id_token"`
	}
	err = json.Unmarshal(body, &jsonResponse)
	if err != nil {
		return
	}
	var email string
	email, err = emailFromIdToken(jsonResponse.IdToken)
	if err != nil {
		return
	}
	s = &SessionState{
		AccessToken:  jsonResponse.AccessToken,
		ExpiresOn:    time.Now().Add(time.Duration(jsonResponse.ExpiresIn) * time.Second).Truncate(time.Second),
		RefreshToken: jsonResponse.RefreshToken,
		Email:        email,
	}
	return
}

// SetGroupRestriction configures the GoogleProvider to restrict access to the
// specified group(s). AdminEmail has to be an administrative email on the domain that is
// checked. CredentialsFile is the path to a json file containing a Google service
// account credentials.
func (p *GoogleProvider) SetGroupRestriction(groups []string, adminEmail string, credentialsReader io.Reader) {
	adminService := getAdminService(adminEmail, credentialsReader)
	p.GroupValidator = func(session *SessionState) bool {
		userGroups := userInGroup(adminService, groups, session.Email)
		if len(userGroups) > 0 {
			session.Groups = userGroups
			return true
		}
		return false
	}
}

func getAdminService(adminEmail string, credentialsReader io.Reader) *admin.Service {
	data, err := ioutil.ReadAll(credentialsReader)
	if err != nil {
		log.Fatal("can't read Google credentials file:", err)
	}
	conf, err := google.JWTConfigFromJSON(data, admin.AdminDirectoryUserReadonlyScope, admin.AdminDirectoryGroupReadonlyScope)
	if err != nil {
		log.Fatal("can't load Google credentials file:", err)
	}
	conf.Subject = adminEmail

	client := conf.Client(oauth2.NoContext)
	adminService, err := admin.New(client)
	if err != nil {
		log.Fatal(err)
	}
	return adminService
}

func userInGroup(service *admin.Service, groups []string, email string) (ret []string) {
	ret = []string{}
	user, err := fetchUser(service, email)
	if err != nil {
		log.Printf("error fetching user: %v", err)
		return
	}
	id := user.Id
	custID := user.CustomerId

	for _, group := range groups {
		members, err := fetchGroupMembers(service, group)
		if err != nil {
			if err, ok := err.(*googleapi.Error); ok && err.Code == 404 {
				log.Printf("error fetching members for group %s: group does not exist", group)
			} else {
				log.Printf("error fetching group members: %v", err)
				return
			}
		}
	membersearch:
		for _, member := range members {
			switch member.Type {
			case "CUSTOMER":
				if member.Id == custID {
					ret = append(ret, group)
					break membersearch
				}
			case "USER":
				if member.Id == id {
					ret = append(ret, group)
					break membersearch
				}
			}
		}
	}
	return
}

func fetchUser(service *admin.Service, email string) (*admin.User, error) {
	user, err := service.Users.Get(email).Do()
	return user, err
}

func fetchGroupMembers(service *admin.Service, group string) ([]*admin.Member, error) {
	members := []*admin.Member{}
	pageToken := ""
	for {
		req := service.Members.List(group)
		if pageToken != "" {
			req.PageToken(pageToken)
		}
		r, err := req.Do()
		if err != nil {
			return nil, err
		}
		for _, member := range r.Members {
			members = append(members, member)
		}
		if r.NextPageToken == "" {
			break
		}
		pageToken = r.NextPageToken
	}
	return members, nil
}

// ValidateGroup validates that the provided email exists in the configured Google
// group(s).
func (p *GoogleProvider) ValidateGroup(session *SessionState) bool {
	return p.GroupValidator(session)
}

func (p *GoogleProvider) RefreshSessionIfNeeded(s *SessionState) (bool, error) {
	if s == nil || s.ExpiresOn.After(time.Now()) || s.RefreshToken == "" {
		return false, nil
	}

	newToken, duration, err := p.redeemRefreshToken(s.RefreshToken)
	if err != nil {
		return false, err
	}

	// re-check that the user is in the proper google group(s)
	if !p.ValidateGroup(s) {
		return false, fmt.Errorf("%s is no longer in the group(s)", s.Email)
	}

	origExpiration := s.ExpiresOn
	s.AccessToken = newToken
	s.ExpiresOn = time.Now().Add(duration).Truncate(time.Second)
	log.Printf("refreshed access token %s (expired on %s)", s, origExpiration)
	return true, nil
}

func (p *GoogleProvider) redeemRefreshToken(refreshToken string) (token string, expires time.Duration, err error) {
	// https://developers.google.com/identity/protocols/OAuth2WebServer#refresh
	params := url.Values{}
	params.Add("client_id", p.ClientID)
	params.Add("client_secret", p.ClientSecret)
	params.Add("refresh_token", refreshToken)
	params.Add("grant_type", "refresh_token")
	var req *http.Request
	req, err = http.NewRequest("POST", p.RedeemURL.String(), bytes.NewBufferString(params.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	var body []byte
	body, err = ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return
	}

	if resp.StatusCode != 200 {
		err = fmt.Errorf("got %d from %q %s", resp.StatusCode, p.RedeemURL.String(), body)
		return
	}

	var data struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	err = json.Unmarshal(body, &data)
	if err != nil {
		return
	}
	token = data.AccessToken
	expires = time.Duration(data.ExpiresIn) * time.Second
	return
}

// SetGroupRestrictionByScript configures the GoogleProvider to restrict access to the
// specified group(s). Google Apps Scripts that returns groups has to be setup, and ScriptId
// and ScriptFunctionName has to be configured.
func (p *GoogleProvider) SetGroupRestrictionGAS(groups []string, scriptId string, functionName string) {

	groupValidationScriptId := scriptId
	groupValidationFunctionName := functionName

	p.GroupValidator = func(session *SessionState) bool {
		userGroups := p.fetchValidGroupsGAS(session, groups, groupValidationScriptId, groupValidationFunctionName)
		if len(userGroups) > 0 {
			session.Groups = userGroups
			return true
		}
		return false
	}
}

func (p *GoogleProvider) getScriptService(s *SessionState) (*script.Service, error) {
	conf := &oauth2.Config{
		ClientID:     p.ClientID,
		ClientSecret: p.ClientSecret,
	}
	token := &oauth2.Token{
		AccessToken:  s.AccessToken,
		TokenType:    "Bearer",
		RefreshToken: s.RefreshToken,
		Expiry:       s.ExpiresOn,
	}
	client := conf.Client(oauth2.NoContext, token)

	return script.New(client)
}

func (p *GoogleProvider) fetchValidGroupsGAS(s *SessionState, groups []string, scriptId string, functionName string) (ret []string) {
	service, err := p.getScriptService(s)
	ret = []string{}
	if err != nil {
		log.Printf("error fetching groups: %v", err)
		return
	}

	grps, err := p.fetchGroupsGAS(service, s.Email, scriptId, functionName)
	if err != nil {
		log.Printf("error fetching groups: %v", err)
		return
	}

	grpSet := make(map[string]struct{})
	for _, g := range grps {
		grpSet[g] = struct{}{}
	}

	for _, g := range groups {
		if _, ok := grpSet[g]; ok {
			ret = append(ret, g)
		}
	}

	return
}

func (p *GoogleProvider) fetchGroupsGAS(service *script.Service, email string, scriptId string, functionName string) ([]string, error) {
	req := script.ExecutionRequest{
		Function:   functionName,
		Parameters: []interface{}{email},
	}

	resp, err := service.Scripts.Run(scriptId, &req).Do()
	if err != nil {
		log.Printf("error while calling GAS Execution API: %s", err)
		return nil, err
	}
	if resp.Error != nil {
		log.Printf("error while running GoogleAppsScript: %s", err)
		return nil, errors.New(string(resp.Error.Details[0]))
	}

	var r struct {
		Result []string `json:"result"`
	}
	err = json.Unmarshal(resp.Response, &r)
	if err != nil {
		return nil, err
	}

	return r.Result, nil
}
