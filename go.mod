module github.com/polynetwork/neo3-voter

go 1.17

require (
	github.com/boltdb/bolt v1.3.1
	github.com/ethereum/go-ethereum v1.10.11
	github.com/joeqian10/EasyLogger v1.0.0
	github.com/joeqian10/neo3-gogogo v1.1.2
	github.com/ontio/ontology-crypto v1.2.1
	github.com/urfave/cli v1.22.4
)

replace (
	github.com/ethereum/go-ethereum v1.10.11 => github.com/polynetwork/Zion v0.0.2-0.20220610105057-6e3d53dde057
)
