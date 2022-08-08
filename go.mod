module github.com/polynetwork/neo3-voter

go 1.18

require (
	github.com/boltdb/bolt v1.3.1
	github.com/ethereum/go-ethereum v1.10.11
	github.com/joeqian10/EasyLogger v1.0.0
	github.com/joeqian10/neo3-gogogo v1.1.2
	github.com/ontio/ontology-crypto v1.2.1
	github.com/urfave/cli v1.22.4
)

replace (
	github.com/ethereum/go-ethereum v1.10.11 => github.com/dylenfu/Zion v0.0.0-20220804061921-6f73ae831a77
	github.com/tendermint/tm-db/064 => github.com/tendermint/tm-db v0.6.4
)
