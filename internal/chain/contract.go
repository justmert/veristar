package chain

import (
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// PublicNetwork is the passphrase of stellar mainnet, the only network
// whose ids enrollment accepts.
const PublicNetwork = network.PublicNetworkPassphrase

// ContractIDFromAddress derives the contract id that a deployer address
// and salt produce on a network: sha-256 of the xdr HashIdPreimage of
// type ENVELOPE_TYPE_CONTRACT_ID. the id depends on nothing else, so a
// matching id proves that this address deployed the contract.
func ContractIDFromAddress(passphrase string, deployer xdr.ScAddress, salt xdr.Uint256) (string, error) {
	pre := xdr.HashIdPreimage{
		Type: xdr.EnvelopeTypeEnvelopeTypeContractId,
		ContractId: &xdr.HashIdPreimageContractId{
			NetworkId: network.ID(passphrase),
			ContractIdPreimage: xdr.ContractIdPreimage{
				Type:        xdr.ContractIdPreimageTypeContractIdPreimageFromAddress,
				FromAddress: &xdr.ContractIdPreimageFromAddress{Address: deployer, Salt: salt},
			},
		},
	}
	b, err := pre.MarshalBinary()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return strkey.Encode(strkey.VersionByteContract, sum[:])
}

// Deployment is one contract creation found in a transaction.
type Deployment struct {
	Contract string
	Deployer string
}

// Deployments lists the contracts a transaction envelope created from an
// address, with their deployer. creations from an asset (stellar asset
// contracts) have no deployer and are left out.
func Deployments(passphrase, envelopeXDR string) ([]Deployment, error) {
	var env xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(envelopeXDR, &env); err != nil {
		return nil, fmt.Errorf("chain: decode envelope: %w", err)
	}
	var out []Deployment
	for _, op := range env.Operations() {
		ih, ok := op.Body.GetInvokeHostFunctionOp()
		if !ok {
			continue
		}
		var pre *xdr.ContractIdPreimage
		switch ih.HostFunction.Type {
		case xdr.HostFunctionTypeHostFunctionTypeCreateContract:
			pre = &ih.HostFunction.CreateContract.ContractIdPreimage
		case xdr.HostFunctionTypeHostFunctionTypeCreateContractV2:
			pre = &ih.HostFunction.CreateContractV2.ContractIdPreimage
		default:
			continue
		}
		if pre.Type != xdr.ContractIdPreimageTypeContractIdPreimageFromAddress || pre.FromAddress == nil {
			continue
		}
		id, err := ContractIDFromAddress(passphrase, pre.FromAddress.Address, pre.FromAddress.Salt)
		if err != nil {
			return nil, err
		}
		dep, err := pre.FromAddress.Address.String()
		if err != nil {
			return nil, err
		}
		out = append(out, Deployment{Contract: id, Deployer: dep})
	}
	return out, nil
}

// CallsContract says whether a transaction envelope invokes a contract
// function. a contract created inside such a call, by a factory contract,
// appears in no operation of the envelope, so Deployments cannot see it.
func CallsContract(envelopeXDR string) (bool, error) {
	var env xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(envelopeXDR, &env); err != nil {
		return false, fmt.Errorf("chain: decode envelope: %w", err)
	}
	for _, op := range env.Operations() {
		if ih, ok := op.Body.GetInvokeHostFunctionOp(); ok && ih.HostFunction.Type == xdr.HostFunctionTypeHostFunctionTypeInvokeContract {
			return true, nil
		}
	}
	return false, nil
}

// adminKeys are the instance storage keys read as the contract's admin or
// owner. soroban has no standard admin entry; these are the conventions of
// the soroban examples and the stellar asset contract (symbol "Admin") and
// of openzeppelin's stellar contracts (enum variant "Owner", stored as a
// one-element vector of the symbol), plus their lowercase spellings.
var adminKeys = []string{"Admin", "admin", "Owner", "owner"}

// ErrNoAdmin is a contract instance without any admin key this reader
// knows.
var ErrNoAdmin = errors.New("chain: the contract instance has no admin or owner entry")

// Admin is an admin entry of a contract instance.
type Admin struct {
	Key     string
	Address string
}

// InstanceKey is the ledger key of a contract's instance entry, base64
// xdr, for getLedgerEntries.
func InstanceKey(contract string) (string, error) {
	raw, err := strkey.Decode(strkey.VersionByteContract, contract)
	if err != nil {
		return "", fmt.Errorf("chain: %s is not a contract id: %w", contract, err)
	}
	var cid xdr.ContractId
	copy(cid[:], raw)
	key := xdr.LedgerKey{
		Type: xdr.LedgerEntryTypeContractData,
		ContractData: &xdr.LedgerKeyContractData{
			Contract:   xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &cid},
			Key:        xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance},
			Durability: xdr.ContractDataDurabilityPersistent,
		},
	}
	return xdr.MarshalBase64(key)
}

// Admins reads the admin entries of a contract instance ledger entry
// (base64 xdr LedgerEntryData, as getLedgerEntries returns it).
func Admins(entryXDR string) ([]Admin, error) {
	var data xdr.LedgerEntryData
	if err := xdr.SafeUnmarshalBase64(entryXDR, &data); err != nil {
		return nil, fmt.Errorf("chain: decode ledger entry: %w", err)
	}
	cd, ok := data.GetContractData()
	if !ok {
		return nil, errors.New("chain: the ledger entry is not contract data")
	}
	inst, ok := cd.Val.GetInstance()
	if !ok {
		return nil, errors.New("chain: the ledger entry is not a contract instance")
	}
	if inst.Storage == nil {
		return nil, ErrNoAdmin
	}
	var out []Admin
	for _, e := range *inst.Storage {
		name, ok := keyName(e.Key)
		if !ok || !isAdminKey(name) {
			continue
		}
		addr, ok := e.Val.GetAddress()
		if !ok {
			continue
		}
		s, err := addr.String()
		if err != nil {
			continue
		}
		out = append(out, Admin{Key: name, Address: s})
	}
	if len(out) == 0 {
		return nil, ErrNoAdmin
	}
	return out, nil
}

// keyName reads a symbol key, or a one-element vector of a symbol (how a
// rust enum unit variant such as Owner is stored).
func keyName(v xdr.ScVal) (string, bool) {
	if s, ok := v.GetSym(); ok {
		return string(s), true
	}
	vec, ok := v.GetVec()
	if !ok || vec == nil || len(*vec) != 1 {
		return "", false
	}
	s, ok := (*vec)[0].GetSym()
	return string(s), ok
}

func isAdminKey(s string) bool {
	for _, k := range adminKeys {
		if s == k {
			return true
		}
	}
	return false
}
