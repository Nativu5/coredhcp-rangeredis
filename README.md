# coredhcp-rangeredis
rangeredis plugin of [CoreDHCP](https://github.com/coredhcp/coredhcp). 

## Introduction

This repo contains a CoreDHCP plugin, which could allocate IP addresses in a given range and save the address assigning status into Redis. 

By utilizing the feature of [Redis keyspace notifications](https://redis.io/docs/manual/keyspace-notifications/), this plugin could be easily integrated into a large system. 

The plugin could not run independently. It must be compiled with CoreDHCP and presented in CoreDHCP configuration. 

## Usage

1. Generate a customized CoreDHCP source code. 
The guide to compile CoreDHCP with third-party plugin could be found at [here](https://github.com/coredhcp/coredhcp/tree/master/cmds/coredhcp-generator). 

2. After a customized `coredhcp.go` is generated, you could clone this repo into the directory.

3. Adjust the `import` part of `coredhcp.go` file. Modify the entry of rangeredis plugin. 

4. Run go mod to get dependency for compiling.

```bash
go mod init coredhcpcomplie
go mod tidy
```

5. Compile:

```bash
go build
```

6. Add config.yaml & run the CoreDHCP. The example on how to config CoreDHCP with rangeredis is [here](https://github.com/sjtu-ctf-platform/coredhcp-rangeredis/blob/main/config.yml.example). 


## Credit

The implementation of this plugin highly relies on the works of [range](https://github.com/coredhcp/coredhcp/tree/master/plugins/range) plugin. 
