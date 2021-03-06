package command

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

var (
	_ssh ssh_config
	Ssh  = &cobra.Command{
		Use: `ssh`,
		Example: `	-C iplist -c ls
	-C iplist -s main.go -d /tmp
	-c ls -u root -p 123456 -H 192.168.1.2:22
	-c ls -u root -P id_rsa -H 192.168.0.129:22
	-u root -p 123456 -H 192.168.1.2:22 -s main.go -d /tmp`,
		RunE:  ssh_run,
		Short: "使用ssh协议群发命令或发送文件",
		Long: `	通过ssh协议群发命令,每个命令发送都是新的session,当从文件读取主机地址和账户密码的时候,格式为IP:PORT USERNAME PASSWD,使用空白分割,-u -p -H 参数不生效,当发送文件的时候目标的地址可以是目录,当是目录的时候保存的文件名,保存为发送的文件名称.`,
	}
)

type ssh_config struct {
	config, out  string
	hosts, cmd   string
	sfile, dpath string
	user, passwd string
	privatekey   string
	timeout      int
	hostfile     bool
}

func init() {
	Ssh.PersistentFlags().StringVarP(&_ssh.config, "hosts", "C", "", `从文件读取主机列表和账户密码`)
	Ssh.PersistentFlags().StringVarP(&_ssh.cmd, "cmd", "c", "", `要执行的命令`)
	Ssh.PersistentFlags().StringVarP(&_ssh.hosts, "host", "H", "", `指定Host,多个地址可使用','分割`)
	Ssh.PersistentFlags().StringVarP(&_ssh.sfile, "src", "s", "", `指定要发送文件的路径`)
	Ssh.PersistentFlags().StringVarP(&_ssh.dpath, "dst", "d", "", `指定文件保存路径`)
	Ssh.PersistentFlags().StringVarP(&_ssh.user, "user", "u", "", `指定登录的用户`)
	Ssh.PersistentFlags().StringVarP(&_ssh.passwd, "passwd", "p", "", `指定登录用户密码`)
	Ssh.PersistentFlags().StringVarP(&_ssh.privatekey, "private", "P", "", `使用私钥登录服务器`)
	Ssh.PersistentFlags().StringVarP(&_ssh.out, "out", "o", "", `指定结果输出文件,不指定则直接输出到标准输出`)
	Ssh.PersistentFlags().BoolVarP(&_ssh.hostfile, "hostfile", "f", false, `指定Host从文件读取,指定次参数,-H参数必须是文件路径`)
	Ssh.PersistentFlags().IntVarP(&_ssh.timeout, "timeout", "t", 30, `指定连接超时时间`)
}

func ssh_run(cmd *cobra.Command, arg []string) error {
	var err error
	var host []string
	var hosts [][]string

	if _ssh.hosts == "" && _ssh.config == "" {
		return fmt.Errorf("参数错误,必须指定主机地址或主机配置文件")
	} else {
		if _ssh.hosts != "" && !_ssh.hostfile {
			host = strings.Split(_ssh.hosts, ",")
		} else {
			if _ssh.hostfile {
				hosts, err = FileLine(_ssh.config, 1)
				host = make([]string, 0, len(hosts))
				for _, h := range hosts {
					host = append(host, h[0])
				}
			} else {
				hosts, err = FileLine(_ssh.config, 3)
			}
			if err != nil {
				fmt.Printf("读取主机列表失败,%s\n", err.Error())
				return nil
			}
		}
		if len(host) <= 0 && len(hosts) <= 0 {
			fmt.Println("主机列表为空")
			return nil
		}
	}

	var output = os.Stdout
	defer output.Close()

	if _ssh.out != "" {
		output, err = os.Create(_ssh.out)
		if err != nil {
			fmt.Printf("创建结果文件失败:%s\n", err.Error())
			return nil
		}
	}
	if _ssh.cmd == "" && (_ssh.sfile == "" || _ssh.dpath == "") {
		return fmt.Errorf("参数错误\n")
	}

	wait := new(sync.WaitGroup)
	if host != nil {
		if _ssh.user == "" || _ssh.passwd == "" && _ssh.privatekey == "" {
			return fmt.Errorf("必须指定用户名,密码或私钥")
		}
		for _, h := range host {
			c, err := newsshInfo(&_ssh, h)
			if err != nil {
				fmt.Println(err.Error())
				return nil
			}
			wait.Add(1)
			if _ssh.cmd != "" {
				go sendcommand(_ssh.cmd, wait, c, output)
			} else {
				go sendfile(_ssh.sfile, _ssh.dpath, wait, c, output)
			}
		}
	} else {
		for _, info := range hosts {
			_ssh.user = info[1]
			_ssh.passwd = info[2]
			c, err := newsshInfo(&_ssh, info[0])
			if err != nil {
				fmt.Println(err.Error())
				return nil
			}

			wait.Add(1)
			if _ssh.cmd != "" {
				go sendcommand(_ssh.cmd, wait, c, output)
			} else {
				go sendfile(_ssh.sfile, _ssh.dpath, wait, c, output)
			}
		}
	}
	wait.Wait()
	return nil
}

func sendcommand(cmd string, wait *sync.WaitGroup, c *sshInfof, w io.Writer) {
	defer wait.Done()
	buf, err := c.SendCommand(cmd)
	if err != nil {
		if _, ok := err.(*net.OpError); ok {
			fmt.Fprintf(w, "连接:%s失败,错误信息:%s\n", c.host, err.Error())
		} else {
			fmt.Fprintf(w, "主机:%s执行命令失败,错误信息:%s\n", c.host, err.Error())
		}
		return
	}
	fmt.Fprintf(w, "%s执行结果:\n%s\n", c.host, string(buf))
}

func sendfile(sfile, dpath string, wait *sync.WaitGroup, c *sshInfof, w io.Writer) {
	defer wait.Done()
	err := c.SendFile(sfile, dpath)
	if err != nil {
		if _, ok := err.(*net.OpError); ok {
			fmt.Fprintf(w, "连接:%s失败,错误信息:%s\n", c.host, err.Error())
		} else {
			fmt.Fprintf(w, "主机:%s发送文件失败,错误信息:%s\n", c.host, err.Error())
		}
	}
	fmt.Fprintf(w, "%s发送文件成功 \n", c.host)
}

func newsshInfo(info *ssh_config, host string) (*sshInfof, error) {
	if info == nil {
		return nil, fmt.Errorf("base config can't null")
	}
	cfg := &ssh.ClientConfig{
		User:    info.user,
		Auth:    []ssh.AuthMethod{},
		Timeout: time.Duration(info.timeout) * time.Second,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			return nil
		},
	}
	if info.privatekey != "" {
		buf, err := ioutil.ReadFile(info.privatekey)
		if err != nil {
			return nil, fmt.Errorf("Read private key error:%s", err.Error())
		}
		sig, err := ssh.ParsePrivateKey(buf)
		if err != nil {
			return nil, fmt.Errorf("Parse private key error:%s", err.Error())
		}
		cfg.Auth = append(cfg.Auth, ssh.PublicKeys(sig))
	}

	if info.passwd != "" {
		cfg.Auth = append(cfg.Auth, ssh.Password(info.passwd))
	}

	return &sshInfof{host: host, config: cfg}, nil
}

type sshInfof struct {
	host   string
	client *ssh.Client
	config *ssh.ClientConfig
}

func (info *sshInfof) Dial() (err error) {
	info.client, err = ssh.Dial("tcp", info.host, info.config)
	return
}

func (info *sshInfof) SendCommand(cmd string) ([]byte, error) {
	if info.client == nil {
		if err := info.Dial(); err != nil {
			return nil, err
		}
	}
	session, err := info.client.NewSession()
	if err != nil {
		return nil, err
	}
	return session.CombinedOutput(cmd)
}

func (info *sshInfof) SendFile(file, dirpath string) error {
	if info.client == nil {
		if err := info.Dial(); err != nil {
			return err
		}
	}

	File, err := os.Open(file)
	if err != nil {
		return err
	}

	defer File.Close()
	session, err := info.client.NewSession()
	if err != nil {
		return err
	}

	defer session.Close()
	stat, _ := File.Stat()

	go func() {
		w, _ := session.StdinPipe()
		fmt.Fprintln(w, "C0644", stat.Size(), filepath.Base(File.Name()))
		io.CopyN(w, File, stat.Size())
		fmt.Fprint(w, "\x00")
		w.Close()
	}()

	err = session.Run(fmt.Sprintf("/usr/bin/scp -qrt %s", dirpath))
	return err
}

func (info *sshInfof) Close() {
	if info.client != nil {
		info.client.Close()
	}
}

func FileLine(path string, count int) ([][]string, error) {
	File, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer File.Close()
	var list [][]string
	buf := bufio.NewReader(File)
	for {
		line, _, err := buf.ReadLine()
		if err != nil {
			if err != io.EOF {
				return list, err
			}
			return list, nil
		}
		l := strings.Fields(string(line))
		if len(l) == count {
			list = append(list, l)
		} else {
			fmt.Printf("无效的数据:%s\n", string(line))
		}
	}
}
