package handy

import (
    "net"
    "net/http"
    "log"
    "path/filepath"
    "fmt"
    "strings"
    "io"
)

type dirInfo struct {
    handler http.Handler
    dir, base, prefix string
}

type Handler struct {
    server *http.Server
    listener net.Listener
    handlers map[string]*dirInfo
    dirs map[string]string
    logger *log.Logger
}


const HANDY_DEFAULT_PORT = 9696
var (
    gLastPort int = 0
    gServers map[int]*Handler = make(map[int]*Handler)
)

func New(port int, logger *log.Logger) *Handler {
    if port < 0 || port > 65535 {
        logger.Println("invalid port", port)
        return nil
    }

    if port == 0 {
        if gLastPort == 0 {
            port = 80
        } else {
            port = gLastPort
        }
    }

    handy := &Handler{
        handlers: make(map[string]*dirInfo),
        dirs: make(map[string]string),
        logger: logger,
    }
    server := &http.Server {
        Addr: fmt.Sprintf(":%d", port),
        Handler: handy,
    }
    handy.server = server

    var e error
    if handy.listener, e = net.Listen("tcp", server.Addr); e != nil {
        // if 80 doesn't work and it's the first run, try to use port 9696 instead
        if gLastPort == 0 && port == 80 {
            logger.Println("cannot listen port", server.Addr, "err:", e)
            logger.Println("trying to use alternative port 9696")

            port = HANDY_DEFAULT_PORT
            server.Addr = fmt.Sprintf(":%d", port)

            if handy.listener, e = net.Listen("tcp", server.Addr); e != nil {
                logger.Println("cannot listen port", server.Addr, "err:", e)
                return nil
            }

            fmt.Println("cannot use default port 80. use alternative port 9696 now.")
        } else {
            logger.Println("cannot listen port", server.Addr, "err:", e)
            return nil
        }
    }

    gLastPort = port
    gServers[port] = handy
    return handy
}

func List(w io.Writer) {
    fmt.Fprintln(w, "Total listened ports:", len(gServers))
    fmt.Fprintln(w, "")

    for k, v := range(gServers) {
        fmt.Fprintln(w, "Port", k)
        v.List(w)
    }
}

func Find(port int) (*Handler, bool) {
    if port == 0 {
        port = gLastPort
    }

    handler, ok := gServers[port]

    if !ok {
        return nil, false
    }

    return handler, true
}

func Stop() {
    for _, v := range(gServers) {
        v.Stop()
    }
}

func LastPort() int {
    return gLastPort
}

func (handy *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // list all aliases if URL is "/"
    if r.URL.Path == "/" || len(r.URL.Path) == 0 {
        w.Header().Set("Content-Type", "text/html; charset=utf-8")
        fmt.Fprintln(w, "<pre>")

        for k, v := range(handy.dirs) {
            fmt.Fprintf(w, `<a href="/%s/">%s</a>`, v, k)
        }

        fmt.Fprintln(w, "</pre>")
        return
    }

    path := r.URL.Path + "/"
    handy.logger.Println("handling a request with path", path)

    for _, v := range(handy.handlers) {
        if strings.HasPrefix(path, v.prefix) {
            r.URL.Path = "/" + v.base + path[len(v.prefix) - 1:len(path) - 1]
            handy.logger.Println("found a matched handler to serve request. path:")
            v.handler.ServeHTTP(w, r)
            return
        }
    }

    handy.logger.Println("no proper handler to serve path", path)
    http.NotFound(w, r)
}

func (handy *Handler) Add(path, alias string) {
    dir := filepath.Dir(path)
    alias = strings.Trim(alias, "/")
    handy.handlers[alias] = &dirInfo {
        handler: http.FileServer(http.Dir(dir)),
        dir: dir,
        base: filepath.Base(path),
        prefix: "/" + alias + "/",
    }
    handy.dirs[dir] = alias

    handy.logger.Println("added new path", path, "with alias", alias)
}

func (handy *Handler) Del(path string) {
    dir := filepath.Dir(path)

    if alias, ok := handy.dirs[dir]; ok {
        alias = strings.Trim(alias, "/")
        delete(handy.handlers, alias)
        delete(handy.dirs, dir)

        handy.logger.Println("deleted path", path, "with alias", alias)
    }
}

func (handy *Handler) Start() {
    go handy.server.Serve(handy.listener)
}

func (handy *Handler) Stop() {
    handy.listener.Close()
    handy.server = nil
}

func (handy *Handler) List(w io.Writer) {
    for k, v := range(handy.dirs) {
        fmt.Fprintf(w, "  /%s\tmapped to %s\n", v, k)
    }
}
