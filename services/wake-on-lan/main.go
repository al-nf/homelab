package main

import (
    "fmt"
    "log"
    "net/http"
    "os/exec"
)

func wakeHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        w.WriteHeader(http.StatusMethodNotAllowed)
        fmt.Fprintln(w, "Use POST")
        return
    }

    mac := "F4:93:9F:F5:7B:CC"
    cmd := exec.Command("wakeonlan", mac)
    if err := cmd.Run(); err != nil {
        w.WriteHeader(http.StatusInternalServerError)
        fmt.Fprintf(w, "Failed to send WOL packet: %v\n", err)
        return
    }

    fmt.Fprintln(w, "WOL packet sent successfully.")
}

func frontendHandler(w http.ResponseWriter, r *http.Request) {
    html := `<html>
<head>
<title>Wake on LAN</title>
<style>
body { font-family: sans-serif; display:flex; flex-direction:column; justify-content:center; align-items:center; height:100vh; background:#f0f0f0; }
button { padding:1em 2em; font-size:1.2em; border-radius:8px; border:none; margin:0.5em; cursor:pointer; background:#4CAF50; color:white; }
button:hover { background:#45a049; }
</style>
</head>
<body>
<button onclick="fetch('/wake',{method:'POST'}).then(r=>r.text()).then(alert)">Wake</button>
</body>
</html>`
    fmt.Fprint(w, html)
}

func main() {
    http.HandleFunc("/", frontendHandler)
    http.HandleFunc("/wake", wakeHandler)

    port := ":8080"
    log.Println("Serving on port", port)
    log.Fatal(http.ListenAndServe(port, nil))
}
