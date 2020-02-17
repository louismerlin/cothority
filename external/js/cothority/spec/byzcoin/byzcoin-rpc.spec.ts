import Long from "long";
import { Log } from "../../src";
import { ByzCoinRPC } from "../../src/byzcoin";
import { LocalCache } from "../../src/byzcoin/byzcoin-rpc";
import DarcInstance from "../../src/byzcoin/contracts/darc-instance";
import Instance from "../../src/byzcoin/instance";
import { Darc } from "../../src/darc";
import { BLOCK_INTERVAL, ROSTER, SIGNER, startConodes } from "../support/conondes";

describe("ByzCoinRPC Tests", () => {
    const roster = ROSTER.slice(0, 4);

    beforeAll(async () => {
        await startConodes();
    });

    it("should create an rpc and evolve/spawn darcs", async () => {
        expect(() => ByzCoinRPC.makeGenesisDarc([], roster)).toThrow();

        const darc = ByzCoinRPC.makeGenesisDarc([SIGNER], roster);
        const rpc = await ByzCoinRPC.newByzCoinRPC(roster, darc, BLOCK_INTERVAL);

        const proof = await rpc.getProof(Buffer.alloc(32, 0));
        expect(proof).toBeDefined();

        const instance = await DarcInstance.fromByzcoin(rpc, darc.getBaseID());

        const evolveDarc = darc.evolve();
        const evolveInstance = await instance.evolveDarcAndWait(evolveDarc, [SIGNER], 10);
        expect(evolveInstance.darc.getBaseID()).toEqual(darc.getBaseID());

        await evolveInstance.update();

        const newDarc = ByzCoinRPC.makeGenesisDarc([SIGNER], roster, "another darc");
        const newInstance = await instance.spawnDarcAndWait(newDarc, [SIGNER], 10);
        expect(newInstance.darc.getBaseID().equals(newDarc.getBaseID())).toBeTruthy();
    });

    it("should create an rpc and get it from byzcoin", async () => {
        const darc = ByzCoinRPC.makeGenesisDarc([SIGNER], roster);
        const rpc = await ByzCoinRPC.newByzCoinRPC(roster, darc, BLOCK_INTERVAL);

        const rpc2 = await ByzCoinRPC.fromByzcoin(roster, rpc.getGenesis().hash);
        await rpc2.updateConfig();

        expect(rpc.getDarc().id).toEqual(rpc2.getDarc().id);
        expect(rpc2.getConfig().blockInterval.toNumber()).toEqual(rpc.getConfig().blockInterval.toNumber());
    });

    it("should throw an error for non-existing instance or wrong type", async () => {
        const darc = ByzCoinRPC.makeGenesisDarc([SIGNER], roster);
        const rpc = await ByzCoinRPC.newByzCoinRPC(roster, darc, BLOCK_INTERVAL);

        await expectAsync(Instance.fromByzcoin(rpc, Buffer.from([1, 2, 3])))
            .toBeRejectedWith(new Error("key not in proof: 010203"));
        await expectAsync(DarcInstance.fromByzcoin(rpc, Buffer.alloc(32, 0))).toBeRejected();
    });

    it("should get updated when an instance is updated", async () => {
        const cache = new LocalCache();
        const darc = ByzCoinRPC.makeGenesisDarc([SIGNER], roster, "initial");
        const rpc = await ByzCoinRPC.newByzCoinRPC(roster, darc, BLOCK_INTERVAL, cache);

        const instUpdate = await rpc.proofObservable(darc.getBaseID());
        const history: string[] = [];
        instUpdate.subscribe((inst) => {
            const d = Darc.decode(inst.value);
            history.push(`${d.version}-${d.description.toString()}`);
        });
        expect(history[0]).toBe("0-initial");

        const di = await DarcInstance.fromByzcoin(rpc, darc.getBaseID());
        const newDI = new Darc({
            ...darc.evolve(),
            description: Buffer.from("new"),
        });
        await di.evolveDarcAndWait(newDI, [SIGNER], 10);

        for (let i = 0; i < 5; i++) {
            if (history.length === 2) {
                break;
            }
            await wait100ms();
        }
        expect(history.length).toBe(2);
        expect(history[1]).toBe("1-new");

        const newDarc = Darc.createBasic([SIGNER], [SIGNER],
            Buffer.from("darc 2"));
        await di.spawnDarcAndWait(newDarc, [SIGNER], 2);
        for (let i = 0; i < 5; i++) {
            expect(history.length).toBe(2);
            await wait100ms();
        }

        const latestProof = (await rpc.proofObservable(darc.getBaseID())).getValue();
        expect(latestProof.stateChangeBody.version.equals(Long.fromNumber(1)))
            .toBeTruthy();

        rpc.closeNewBlocks();
    });
});

async function wait100ms(): Promise<void> {
    return new Promise((resolve) => setTimeout(resolve, 100));
}
