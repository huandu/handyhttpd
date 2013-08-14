package main

import (
    "flag"
    "log"
    "io"
    "os"
    "os/signal"
    "path/filepath"
    "net"
    "net/url"
    "net/http"
    "syscall"
    "fmt"
    "strconv"
    "strings"
)

const (
    HANDY_SOCK_FILENAME = "handyhttpd.sock"
    HANDY_LOG_FILENAME = "handyhttpd.log"
)

// remember all handler instances
var (
    gLogger *log.Logger
)

func parseParams(root, pattern string, port int, remove bool) bool {
    handy, ok := Find(port)

    if !ok {
        if handy = New(port, gLogger); handy == nil {
            gLogger.Println("cannot create new handy server")
            return false
        }
    }

    if remove {
        handy.Del(root)
    } else {
        handy.Add(root, pattern)
    }

    handy.Start()
    return true
}

func main() {
    port := flag.Int("port", 0, "Port to serve http request. By default, the port is the last port you've used.")
    dir := flag.String("dir", "", "Dir served as www root. By default, current dir will be served.")
    alias := flag.String("alias", "", "URL alias to serve. By default, dir name will be used as alias.")
    remove := flag.Bool("remove", false, "Remove current dir so that no one can visit it thru http anymore.")
    list := flag.Bool("list", false, "List all running servers and hosted dirs.")
    worker := flag.Bool("worker", false, "Indicate it's a worker process. It's used to daemonize handyhttpd. Never use it in command line.")
    quit := flag.Bool("quit", false, "Quit server completely.")
    flag.Parse()

    tempdir := os.TempDir()
    file, err := os.OpenFile(tempdir + "/" + HANDY_LOG_FILENAME, os.O_APPEND | os.O_CREATE | os.O_RDWR, 0666)
    if err != nil {
        log.Println("cannot open log file to write. filename:", tempdir + "/" + HANDY_LOG_FILENAME, "err:", err)
        log.Println("print log to stdout now")
        file = os.Stdout
    }

    // worker need to close all std in/out/err
    if *worker {
        fd, err := syscall.Open("/dev/null", syscall.O_RDWR, 0)

        if err != nil {
            fmt.Println("cannot open /dev/null", "err:", err)
            panic(err)
        }

        syscall.Dup2(fd, syscall.Stdin)
        syscall.Dup2(fd, syscall.Stdout)
        syscall.Dup2(fd, syscall.Stderr)

        if fd > syscall.Stderr {
            syscall.Close(fd)
        }
    }

    gLogger = log.New(file, "", log.LstdFlags)
    gLogger.Printf("parsed params. [port: %d] [dir: %s] [alias: %s] [remove: %t] [list: %t] [quit: %t]\n",
        *port, *dir, *alias, *remove, *list, *quit)

    root := *dir
    if root == "" {
        root, _ = os.Getwd()
    }

    pattern := *alias
    if pattern == "" {
        pattern = filepath.Base(root)
    }

    socket := tempdir + "/" + HANDY_SOCK_FILENAME
    l, err := net.Listen("unix", socket)

    // there is a handyhttpd running. notify it with current options.
    if err != nil {
        // hack http client's transport to force it to use unix socket rather than tcp
        client := http.Client {
            Transport: &http.Transport {
                Dial: func (n, addr string) (conn net.Conn, err error) {
                    return net.Dial("unix", socket)
                },
            },
        }

        var r *http.Response

        if *list {
            r, err = client.Get("http://localhost/list")
        } else if *quit {
            r, err = client.Get("http://localhost/quit")
        } else {
            var verb string
            if *remove {
                verb = "remove"
            } else {
                verb = "add"
            }

            // format is:
            // GET /?verb=add&alias=abc&dir=/path/to/www/root&port=9696
            r, err = client.Get(
                fmt.Sprintf("http://localhost/?verb=%s&alias=%s&dir=%s&port=%d",
                    verb, url.QueryEscape(pattern), url.QueryEscape(root), *port))
        }

        if err != nil {
            gLogger.Println("cannot connect handy server. err:", err)
            return
        }

        if r.StatusCode != 200 {
            gLogger.Println("handy server denies the request. code:", r.StatusCode)
            return
        }

        io.Copy(os.Stdout, r.Body)
        return
    }

    defer l.Close()

    // daemonize handyhttpd
    if !*worker {
        args := append([]string{os.Args[0], "-worker"}, os.Args[1:]...)
        exec := os.Args[0]

        // if exec is called without any path separator, it must be in a PATH dir.
        if !strings.ContainsRune(exec, os.PathSeparator) {
            path := os.Getenv("PATH")
            paths := strings.Split(path, fmt.Sprintf("%c", os.PathListSeparator))

            for _, s := range(paths) {
                if file, err := os.Stat(s + "/" + exec); err == nil && !file.IsDir() {
                    exec = s + "/" + exec
                }
            }
        }

        _, _, err := syscall.StartProcess(exec, args, nil)

        if err != nil {
            fmt.Println("cannot daemonize handyhttpd", "err:", err)
            gLogger.Println("cannot daemonize handyhttpd", "err:", err)
            return
        }

        return
    }

    // there is no running handy, just return
    if *quit {
        gLogger.Println("gracefully exit with command line")
        return
    }

    // as this server is the only running server, nothing to list
    if *list {
        fmt.Println("No server is running")
        return
    }

    parseParams(root, pattern, *port, *remove)

    go func() {
        // handle other server's request
        http.HandleFunc("/list", func(w http.ResponseWriter, r *http.Request) {
            gLogger.Println("listing all ports per request")
            List(w)
        })
        http.HandleFunc("/quit", func(w http.ResponseWriter, r *http.Request) {
            fmt.Fprintln(w, "Handy server is quiting now")

            // gracefully exit
            process, _ := os.FindProcess(os.Getpid())
            process.Signal(syscall.SIGINT)
        })
        http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
            gLogger.Println("handle a verb request. url:", r.URL)
            r.ParseForm()
            verb, verbOk := r.Form["verb"]
            alias, aliasOk := r.Form["alias"]
            dir, dirOk := r.Form["dir"]
            port, portOk := r.Form["port"]

            if !verbOk || !aliasOk || !dirOk || !portOk {
                gLogger.Println("missing required query string params")
                w.WriteHeader(http.StatusBadRequest)
                return
            }

            remove := false
            if verb[0] == "remove" {
                remove = true
            }

            portNumber, _ := strconv.Atoi(port[0])
            if parseParams(dir[0], alias[0], portNumber, remove) {
                fmt.Fprintf(w, "%s dir %s as /%s on port %d\n", verb[0], dir[0], alias[0], LastPort())
            }
        })
        http.Serve(l, nil)
    }()

    defer Stop()
    sig := make(chan os.Signal, 1)
    signal.Notify(sig)

    for {
        s := <-sig
        if s == syscall.SIGKILL || s == syscall.SIGINT || s == syscall.SIGTERM {
            gLogger.Println("gracefully exit with signal", s)
            return
        }
    }
}
