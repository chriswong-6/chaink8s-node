package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"pkg.akt.dev/node/x/chaink8s/webhook"
)

func main() {
	listen   := flag.String("listen",    ":8443",             "HTTPS listen address")
	etcdAddr := flag.String("etcd-addr", "localhost:12379",   "etcd adapter address")
	dnsName  := flag.String("dns-name",  "ck8s-webhook.kube-system.svc", "TLS cert DNS name")
	certOut  := flag.String("cert-out",  "",                  "write CA cert PEM to this file (for webhook config)")
	flag.Parse()

	// 生成自签名 TLS 证书
	tlsCert, caPEM, err := webhook.GenerateSelfSignedTLS([]string{*dnsName, "localhost"})
	if err != nil {
		log.Fatalf("generate TLS cert: %v", err)
	}

	// 可选：输出 CA cert 供注册 ValidatingWebhookConfiguration 使用
	if *certOut != "" {
		if err := os.WriteFile(*certOut, caPEM, 0644); err != nil {
			log.Fatalf("write cert: %v", err)
		}
		log.Printf("CA cert written to %s", *certOut)
	}
	log.Printf("CA cert (base64, for webhookConfig.caBundle):\n%s",
		base64.StdEncoding.EncodeToString(caPEM))

	// 创建 Webhook handler（连接 etcd Adapter）
	h, err := webhook.NewHandler(*etcdAddr)
	if err != nil {
		log.Fatalf("webhook handler: %v", err)
	}

	srv := &http.Server{
		Addr:    *listen,
		Handler: h,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
			MinVersion:   tls.VersionTLS12,
		},
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		log.Printf("ck8s-webhook: listening on %s (etcd-addr=%s)", *listen, *etcdAddr)
		if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.Printf("ck8s-webhook: %v", err)
			cancel()
		}
	}()

	<-ctx.Done()
	log.Println("ck8s-webhook: shutting down")
	if err := srv.Shutdown(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "shutdown: %v\n", err)
	}
}
