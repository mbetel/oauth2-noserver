package oauth2ns

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/fatih/color"
	rndm "github.com/nmrshll/rndm-go"
	"github.com/skratchdot/open-golang/open"
	"golang.org/x/oauth2"
)

type contextKey int

type AuthorizedClient struct {
	*http.Client
	Token *oauth2.Token
}

const (
	// PORT is the port that the temporary oauth server will listen on
	PORT                                  = 14565
	oauthStateStringContextKey contextKey = iota
)

// Authorize starts the login process
func Authorize(conf *oauth2.Config) *AuthorizedClient {
	// add transport for self-signed certificate to context
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	sslcli := &http.Client{Transport: tr}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, sslcli)

	// Redirect user to consent page to ask for permission
	// for the scopes specified above.
	conf.RedirectURL = fmt.Sprintf("http://127.0.0.1:%s/oauth/callback", strconv.Itoa(PORT))

	// Some random string, random for each request
	oauthStateString := rndm.String(8)
	ctx = context.WithValue(ctx, oauthStateStringContextKey, oauthStateString)
	url := conf.AuthCodeURL(oauthStateString, oauth2.AccessTypeOffline)

	quitSignalChan := make(chan struct{})
	clientChan := make(chan *AuthorizedClient)
	startHTTPServer(ctx, conf, clientChan, quitSignalChan)
	log.Println(color.CyanString("You will now be taken to your browser for authentication"))
	time.Sleep(600 * time.Millisecond)
	open.Run(url)
	time.Sleep(600 * time.Millisecond)

	// wait for client on clientChan
	client := <-clientChan
	// When the callbackHandler returns a client, it's time to shutdown the server gracefully
	quitSignalChan <- struct{}{}

	return client
}

func startHTTPServer(ctx context.Context, conf *oauth2.Config, clientChan chan *AuthorizedClient, quitSignalChan chan struct{}) {
	http.HandleFunc("/oauth/callback", callbackHandler(ctx, conf, clientChan))
	srv := &http.Server{Addr: ":" + strconv.Itoa(PORT)}

	go func() {
		// wait for quitSignal on quitSignalChan
		<-quitSignalChan
		log.Println("Shutting down server...")

		d := time.Now().Add(5 * time.Second) // deadline 5s max
		ctx, cancel := context.WithDeadline(context.Background(), d)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			log.Fatalf("could not shutdown: %v", err)
		}
	}()

	go func() {
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
		fmt.Println("Server gracefully stopped")
	}()
}

func callbackHandler(ctx context.Context, conf *oauth2.Config, clientChan chan *AuthorizedClient) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		requestStateString := ctx.Value(oauthStateStringContextKey).(string)
		responseStateString := r.FormValue("state")
		if responseStateString != requestStateString {
			fmt.Printf("invalid oauth state, expected '%s', got '%s'\n", requestStateString, responseStateString)
			http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
			return
		}

		code := r.FormValue("code")
		token, err := conf.Exchange(ctx, code)
		if err != nil {
			fmt.Printf("oauthConf.Exchange() failed with '%s'\n", err)
			http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
			return
		}
		// The HTTP Client returned by conf.Client will refresh the token as necessary
		client := &AuthorizedClient{
			conf.Client(ctx, token),
			token,
		}
		// show success page
		successPage := `
		<div style="height:100px; width:100%!; display:flex; flex-direction: column; justify-content: center; align-items:center; background-color:#2ecc71; color:white; font-size:22"><div>Success!</div></div>
		<p style="margin-top:20px; font-size:18; text-align:center">You are authenticated, you can now return to the program. This will auto-close</p>
		<script>window.onload=function(){setTimeout(this.close, 4000)}</script>
		`
		fmt.Fprintf(w, successPage)
		// quitSignalChan <- quitSignal
		clientChan <- client
	}
}
